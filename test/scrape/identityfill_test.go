// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"context"
	"slices"
	"testing"

	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/identityfillprocessor"
)

// newIdentityFill builds the identityfill processor from its block in the reference config.
func newIdentityFill(t *testing.T, sink *consumertest.LogsSink) processor.Logs {
	t.Helper()
	block := section(t, loadReference(t), "processors", "identityfill")
	factory := identityfillprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal identityfill: %v", err)
	}
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create identityfill: %v", err)
	}
	return proc
}

// A GKE audit entry leaves the decoder with no host.name, service.name or body.
// sourcetag/gcp then identityfill must fill all three.
func TestGKEAuditEntryIsFilledAfterSourceTag(t *testing.T) {
	final := new(consumertest.LogsSink)
	fill := newIdentityFill(t, final)
	mid := new(consumertest.LogsSink)
	tag := newSourceTag(t, "sourcetag/gcp", mid)

	tagged := through(t, gcpDecoder(t), tag, mid, compact(t, readFile(t, "testdata/gcp-audit-gke-entry.json")))
	before := recordsOf(tagged)[0]
	wantAbsent(t, before.resource, "host.name")
	wantAbsent(t, before.resource, "service.name")

	if err := fill.ConsumeLogs(context.Background(), tagged); err != nil {
		t.Fatalf("identityfill: %v", err)
	}
	if len(final.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(final.AllLogs()))
	}
	rec := recordsOf(final.AllLogs()[0])[0]
	wantStr(t, rec.resource, "host.name", "kube-v0")
	wantStr(t, rec.resource, "service.name", "gcp-audit")
	wantStr(t, rec.log.Attributes(), "log.source", "gcp.audit")
	if got := rec.log.Body().Str(); got == "" {
		t.Fatalf("audit body is empty, want the method and resource")
	}
}

// The reference config runs identityfill after sourcetag/gcp.
func TestGCPPipelineRunsIdentityFillAfterSourceTag(t *testing.T) {
	procs := pipelineProcessors(t, "logs/gcp")
	tag, fill := slices.Index(procs, "sourcetag/gcp"), slices.Index(procs, "identityfill")
	if tag < 0 || fill < 0 || fill < tag {
		t.Fatalf("logs/gcp processors = %v, want sourcetag/gcp before identityfill", procs)
	}
}

func pipelineProcessors(t *testing.T, name string) []string {
	t.Helper()
	pipelines := loadReference(t)["service"].(map[string]any)["pipelines"].(map[string]any)
	block, ok := pipelines[name].(map[string]any)
	if !ok {
		t.Fatalf("pipeline %s missing from %s", name, referenceConfig)
	}
	var out []string
	for _, p := range block["processors"].([]any) {
		out = append(out, p.(string))
	}
	return out
}
