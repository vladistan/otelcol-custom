// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

// Blank imports hold the AWS SDK pins from the generated gateway graph
// (07 AD-10) until the poll loop imports them. Remove each with its first use.
import (
	_ "github.com/aws/aws-sdk-go-v2/config"
	_ "github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	_ "github.com/aws/aws-sdk-go-v2/service/sqs"
	_ "github.com/aws/aws-sdk-go-v2/service/sts"
)
