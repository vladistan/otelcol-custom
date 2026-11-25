// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import "testing"

func TestSource_EachRenamedSourceLeavesAsItsNewValueAndTheOthersKeepTheirs(t *testing.T) {
	for _, c := range []struct {
		name, provider string
		in             input
		want           string
	}{
		{name: "cloudtrail", provider: "aws", in: input{scope: map[string]string{"encoding.format": "aws.cloudtrail"}}, want: "cloudtrail"},
		{name: "s3access", provider: "aws", in: input{scope: map[string]string{"encoding.format": "aws.s3access"}}, want: "s3access"},
		{name: "elb", provider: "aws", in: input{scope: map[string]string{"encoding.format": "aws.elbaccess"}}, want: "elb"},
		{name: "eks", provider: "aws", in: input{res: map[string]string{"cloudwatch.log.group.name": "/aws/eks/prod/cluster"}}, want: "eks"},
		{name: "audit", provider: "gcp", in: input{res: map[string]string{"cloud.resource_id": "cloudaudit.googleapis.com%2Factivity"}}, want: "audit"},
		{name: "lb", provider: "gcp", in: input{res: map[string]string{"gcp.resource_type": "http_load_balancer"}}, want: "lb"},
		{name: "cloudwatch keeps its name", provider: "aws", in: input{res: map[string]string{"cloudwatch.log.group.name": "/aws/lambda/api"}}, want: "aws.cloudwatch"},
		{name: "cloudrun keeps its name", provider: "gcp", in: input{res: map[string]string{"gcp.resource_type": "cloud_run_revision"}}, want: "gcp.cloudrun"},
		{name: "azure keeps its name", provider: "azure", in: input{attrs: map[string]string{"log.source": "other"}}, want: "azure.activity"},
		{name: "auth0 keeps its name", provider: "aws", in: input{attrs: map[string]string{"log.source": "auth0"}}, want: "auth0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, lr := tag(t, c.provider, c.in)

			wantStr(t, lr.Attributes(), "log.source", c.want)
		})
	}
}
