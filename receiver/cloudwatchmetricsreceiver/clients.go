// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const sessionName = "otelcol-cloudwatchmetrics"

// metricsAPI is the part of the CloudWatch client the receiver uses.
type metricsAPI interface {
	ListMetrics(ctx context.Context, in *cloudwatch.ListMetricsInput, opts ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricsOutput, error)
	GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, opts ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// clientFactory returns a CloudWatch client for one account and region.
type clientFactory interface {
	client(ctx context.Context, acct AccountConfig, region string) (metricsAPI, error)
}

// awsFactory builds clients from the default credential chain, which in the pod
// is the pod's own role. An account with a role_arn assumes that role through STS
// once and shares the cached credentials across its regions.
type awsFactory struct {
	loadConfig    func(ctx context.Context, region string) (aws.Config, error)
	newSTS        func(aws.Config) stscreds.AssumeRoleAPIClient
	newCloudWatch func(aws.Config) metricsAPI

	mu    sync.Mutex
	creds map[string]aws.CredentialsProvider
}

func newAWSFactory() *awsFactory {
	return &awsFactory{
		loadConfig: func(ctx context.Context, region string) (aws.Config, error) {
			return config.LoadDefaultConfig(ctx, config.WithRegion(region))
		},
		newSTS:        func(cfg aws.Config) stscreds.AssumeRoleAPIClient { return sts.NewFromConfig(cfg) },
		newCloudWatch: func(cfg aws.Config) metricsAPI { return cloudwatch.NewFromConfig(cfg) },
		creds:         map[string]aws.CredentialsProvider{},
	}
}

func (f *awsFactory) client(ctx context.Context, acct AccountConfig, region string) (metricsAPI, error) {
	cfg, err := f.loadConfig(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	if acct.RoleARN != "" {
		provider, err := f.assumed(ctx, acct, cfg)
		if err != nil {
			return nil, err
		}
		cfg.Credentials = provider
	}
	return f.newCloudWatch(cfg), nil
}

// assumed returns the cached credentials of the account's role, fetching them
// now so a refused role shows up here and not at the first API call.
func (f *awsFactory) assumed(ctx context.Context, acct AccountConfig, cfg aws.Config) (aws.CredentialsProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	provider, ok := f.creds[acct.Name]
	if !ok {
		provider = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(f.newSTS(cfg), acct.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = sessionName
			if acct.ExternalID != "" {
				o.ExternalID = aws.String(acct.ExternalID)
			}
		}))
		f.creds[acct.Name] = provider
	}
	if _, err := provider.Retrieve(ctx); err != nil {
		// The provider is bound to this region's STS client. Forget it so the
		// account's next region tries with its own endpoint.
		delete(f.creds, acct.Name)
		return nil, fmt.Errorf("assume role %s: %w", acct.RoleARN, err)
	}
	return provider, nil
}
