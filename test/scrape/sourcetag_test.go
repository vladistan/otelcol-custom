// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
)

// oneThrough runs a hand-built record through a reference-config processor and returns the result.
func oneThrough(t *testing.T, name string, res map[string]any) record {
	t.Helper()
	sink := new(consumertest.LogsSink)
	proc := newSourceTag(t, name, sink)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(res); err != nil {
		t.Fatal(err)
	}
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("line")
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	return recordsOf(sink.AllLogs()[0])[0]
}

func TestEKSAuditStreamIsTaggedAudit(t *testing.T) {
	rec := oneThrough(t, "sourcetag/aws", map[string]any{
		"cloudwatch.log.group.name": "/aws/eks/prod/cluster", "cloudwatch.log.stream": "kube-apiserver-audit-1",
	})

	wantStr(t, rec.log.Attributes(), "log.source", "aws.eks")
	wantStr(t, rec.log.Attributes(), "event.category", "audit")
}

func TestEKSOtherStreamDefaultsToApplication(t *testing.T) {
	rec := oneThrough(t, "sourcetag/aws", map[string]any{
		"cloudwatch.log.group.name": "/aws/eks/prod/cluster", "cloudwatch.log.stream": "authenticator-1",
	})

	wantStr(t, rec.log.Attributes(), "log.source", "aws.eks")
	wantStr(t, rec.log.Attributes(), "event.category", "application")
}

func TestLoadBalancerEntriesAreTaggedGCPLB(t *testing.T) {
	rec := oneThrough(t, "sourcetag/gcp", map[string]any{"gcp.resource_type": "http_load_balancer", "gcp.label.project_id": "p1"})

	wantStr(t, rec.log.Attributes(), "log.source", "gcp.lb")
	wantStr(t, rec.log.Attributes(), "event.category", "access")
	wantStr(t, rec.resource, "cloud.account.id", "p1")
}

func TestAzureRecordsAreTaggedActivityAudit(t *testing.T) {
	rec := oneThrough(t, "sourcetag/azure", nil)

	wantStr(t, rec.log.Attributes(), "log.source", "azure.activity")
	wantStr(t, rec.log.Attributes(), "event.category", "audit")
	wantStr(t, rec.resource, "cloud.provider", "azure")
}
