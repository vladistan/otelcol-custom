// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSTS records every AssumeRole call.
type fakeSTS struct {
	mu    sync.Mutex
	calls []sts.AssumeRoleInput
	err   error
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	return &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
		AccessKeyId: aws.String("AKIATEST"), SecretAccessKey: aws.String("secret"), SessionToken: aws.String("token"),
		Expiration: aws.Time(time.Now().Add(time.Hour)),
	}}, nil
}

func (f *fakeSTS) recorded() []sts.AssumeRoleInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sts.AssumeRoleInput(nil), f.calls...)
}

type factoryEnv struct {
	factory *awsFactory
	sts     *fakeSTS
	api     *fakeAPI
	regions []string // regions the config loader was asked for
}

func newFactoryEnv() *factoryEnv {
	env := &factoryEnv{sts: new(fakeSTS), api: newFakeAPI()}
	env.factory = &awsFactory{
		loadConfig: func(_ context.Context, region string) (aws.Config, error) {
			env.regions = append(env.regions, region)
			return aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider("A", "B", "")}, nil
		},
		newSTS:        func(aws.Config) stscreds.AssumeRoleAPIClient { return env.sts },
		newCloudWatch: func(aws.Config) metricsAPI { return env.api },
		creds:         map[string]aws.CredentialsProvider{},
	}
	return env
}

func TestFactoryAssumesTheRoleOncePerAccountWithRoleAndExternalID(t *testing.T) {
	env := newFactoryEnv()
	acct := AccountConfig{Name: "prod", RoleARN: "arn:aws:iam::111111111111:role/ro", ExternalID: "ext-1", Regions: []string{"us-east-1", "eu-west-1"}}

	for _, region := range acct.Regions {
		api, err := env.factory.client(context.Background(), acct, region)
		require.NoError(t, err)
		assert.NotNil(t, api)
	}

	calls := env.sts.recorded()
	require.Len(t, calls, 1, "one AssumeRole for the account, shared by both regions")
	assert.Equal(t, "arn:aws:iam::111111111111:role/ro", aws.ToString(calls[0].RoleArn))
	assert.Equal(t, "ext-1", aws.ToString(calls[0].ExternalId))
	assert.NotEmpty(t, aws.ToString(calls[0].RoleSessionName))
	assert.Equal(t, []string{"us-east-1", "eu-west-1"}, env.regions)
}

func TestFactoryAssumesOneRolePerAccount(t *testing.T) {
	env := newFactoryEnv()
	prod := AccountConfig{Name: "prod", RoleARN: "arn:aws:iam::111111111111:role/ro", Regions: []string{"us-east-1"}}
	tools := AccountConfig{Name: "tools", RoleARN: "arn:aws:iam::222222222222:role/ro", ExternalID: "x", Regions: []string{"us-east-1"}}

	for _, acct := range []AccountConfig{prod, tools} {
		_, err := env.factory.client(context.Background(), acct, "us-east-1")
		require.NoError(t, err)
	}

	calls := env.sts.recorded()
	require.Len(t, calls, 2)
	assert.Equal(t, "arn:aws:iam::111111111111:role/ro", aws.ToString(calls[0].RoleArn))
	assert.Nil(t, calls[0].ExternalId, "no external id when none is configured")
	assert.Equal(t, "x", aws.ToString(calls[1].ExternalId))
}

func TestFactoryAccountWithoutARoleUsesThePodCredentialsDirectly(t *testing.T) {
	env := newFactoryEnv()

	_, err := env.factory.client(context.Background(), AccountConfig{Name: "home", Regions: []string{"us-east-1"}}, "us-east-1")

	require.NoError(t, err)
	assert.Empty(t, env.sts.recorded())
}

func TestFactoryRefusedRoleIsAnErrorForThatAccount(t *testing.T) {
	env := newFactoryEnv()
	env.sts.err = errors.New("AccessDenied")

	_, err := env.factory.client(context.Background(), AccountConfig{Name: "prod", RoleARN: "arn:aws:iam::1:role/x"}, "us-east-1")

	assert.ErrorContains(t, err, "AccessDenied")
}

// A region whose STS endpoint fails must not poison the account for its other regions.
func TestFactoryAFailingStsRegionDoesNotBreakTheAccountsOtherRegions(t *testing.T) {
	env := newFactoryEnv()
	broken, healthy := &fakeSTS{err: errors.New("endpoint unreachable")}, new(fakeSTS)
	env.factory.newSTS = func(cfg aws.Config) stscreds.AssumeRoleAPIClient {
		if cfg.Region == "ap-gone-1" {
			return broken
		}
		return healthy
	}
	acct := AccountConfig{Name: "prod", RoleARN: "arn:aws:iam::111111111111:role/ro", Regions: []string{"ap-gone-1", "eu-west-1"}}

	_, err := env.factory.client(context.Background(), acct, "ap-gone-1")
	require.ErrorContains(t, err, "endpoint unreachable")
	api, err := env.factory.client(context.Background(), acct, "eu-west-1")
	require.NoError(t, err)
	assert.NotNil(t, api)

	assert.Len(t, healthy.recorded(), 1)
}

func TestFactoryRetriesTheRoleAfterAFailureWithTheNextRegionsClient(t *testing.T) {
	env := newFactoryEnv()
	acct := AccountConfig{Name: "prod", RoleARN: "arn:aws:iam::1:role/x"}
	env.sts.err = errors.New("AccessDenied")
	_, err := env.factory.client(context.Background(), acct, "us-east-1")
	require.Error(t, err)

	env.sts.err = nil
	_, err = env.factory.client(context.Background(), acct, "us-east-1")

	require.NoError(t, err)
	assert.Len(t, env.sts.recorded(), 2)
}

func TestFactoryConfigLoadErrorIsReturned(t *testing.T) {
	env := newFactoryEnv()
	env.factory.loadConfig = func(context.Context, string) (aws.Config, error) { return aws.Config{}, errors.New("no config") }

	_, err := env.factory.client(context.Background(), AccountConfig{Name: "prod"}, "us-east-1")

	assert.ErrorContains(t, err, "no config")
}

func TestNewAWSFactoryUsesTheRealClients(t *testing.T) {
	f := newAWSFactory()

	assert.NotNil(t, f.loadConfig)
	assert.NotNil(t, f.newSTS)
	assert.NotNil(t, f.newCloudWatch)
	cfg, err := f.loadConfig(context.Background(), "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", cfg.Region)
	assert.NotNil(t, f.newSTS(cfg))
	assert.NotNil(t, f.newCloudWatch(cfg))
}
