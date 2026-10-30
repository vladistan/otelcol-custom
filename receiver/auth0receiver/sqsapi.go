// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const roleSessionName = "otelcol-auth0"

// sqsAPI is the part of the SQS client the poll loop uses; tests supply a fake.
type sqsAPI interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, in *sqs.DeleteMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
}

func newSQSClient(ctx context.Context, cfg *Config) (sqsAPI, error) {
	awsCfg, err := loadAWSConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}), nil
}

// loadAWSConfig uses the default credential chain and assumes role_arn when set.
func loadAWSConfig(ctx context.Context, cfg *Config) (aws.Config, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	if cfg.RoleARN == "" {
		return awsCfg, nil
	}
	stsClient := sts.NewFromConfig(awsCfg, func(o *sts.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	provider := stscreds.NewAssumeRoleProvider(stsClient, cfg.RoleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = roleSessionName
		if cfg.ExternalID != "" {
			o.ExternalID = aws.String(cfg.ExternalID)
		}
	})
	awsCfg.Credentials = aws.NewCredentialsCache(provider)
	return awsCfg, nil
}
