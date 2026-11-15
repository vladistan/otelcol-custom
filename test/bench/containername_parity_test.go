// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/containernameprocessor"
)

// containerOTTL is the role-side rule the containername processor replaces.
var containerOTTL = map[string]any{
	"log_statements": []any{
		map[string]any{
			"context": "resource",
			"statements": []any{
				`set(attributes["service.name"], attributes["container.name"]) ` +
					`where attributes["container.name"] != nil and attributes["container.name"] != ""`,
			},
		},
	},
}

func newContainerName(tb testing.TB, next consumer.Logs) processor.Logs {
	tb.Helper()
	f := containernameprocessor.NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		tb.Fatalf("create containernameprocessor: %v", err)
	}
	return p
}

// containerResources returns one resource per entry, each holding one record.
func containerResources(tb testing.TB, resources []map[string]any) plog.Logs {
	tb.Helper()
	ld := plog.NewLogs()
	for _, attrs := range resources {
		rl := ld.ResourceLogs().AppendEmpty()
		if err := rl.Resource().Attributes().FromRaw(attrs); err != nil {
			tb.Fatalf("set resource attributes: %v", err)
		}
		rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("line")
	}
	return ld
}

func TestContainerNameParity_ServiceNameMatchesOTTL(t *testing.T) {
	cases := []map[string]any{
		{"container.name": "web-1", "service.name": "docker.service"},
		{"container.name": "web-1", "service.name": "sshd.service"},
		{"container.name": "web-1"},
		{"service.name": "docker.service"},
		{"container.name": "", "service.name": "docker.service"},
	}
	ottlSink, procSink := new(consumertest.LogsSink), new(consumertest.LogsSink)
	ottl, proc := newOTTLFromBlock(t, ottlSink, containerOTTL), newContainerName(t, procSink)

	for i, attrs := range cases {
		want := runThrough(t, ottl, ottlSink, containerResources(t, []map[string]any{attrs}))
		got := runThrough(t, proc, procSink, containerResources(t, []map[string]any{attrs}))

		wantName, wantOK := want.ResourceLogs().At(0).Resource().Attributes().Get("service.name")
		gotName, gotOK := got.ResourceLogs().At(0).Resource().Attributes().Get("service.name")
		if wantOK != gotOK || (wantOK && wantName.AsString() != gotName.AsString()) {
			t.Errorf("case %d service.name = %v (present %v), OTTL gave %v (present %v)",
				i, gotName.AsString(), gotOK, wantName.AsString(), wantOK)
		}
	}
}

// containerBatch holds batchSize resources: five containers, with every tenth lacking a container name.
func containerBatch(b *testing.B) plog.Logs {
	b.Helper()
	resources := make([]map[string]any, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		attrs := map[string]any{"service.name": "docker.service", "host.name": "host-1"}
		if i%10 != 0 {
			attrs["container.name"] = fmt.Sprintf("ctr-%d", i%5)
		}
		resources = append(resources, attrs)
	}
	return containerResources(b, resources)
}

func BenchmarkContainerName(b *testing.B) {
	template := containerBatch(b)
	nop := consumertest.NewNop()

	b.Run("impl=ottl", func(b *testing.B) {
		run(b, newOTTLFromBlock(b, nop, containerOTTL), template)
	})
	b.Run("impl=containername", func(b *testing.B) {
		run(b, newContainerName(b, nop), template)
	})
}
