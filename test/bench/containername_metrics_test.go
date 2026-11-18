// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/containernameprocessor"
)

// containerMetricsOTTL is the rule the containername processor applies to metrics, written as OTTL.
var containerMetricsOTTL = map[string]any{
	"metric_statements": []any{
		map[string]any{
			"context": "resource",
			"statements": []any{
				`set(attributes["service.name"], attributes["container.name"]) ` +
					`where attributes["container.name"] != nil and attributes["container.name"] != ""`,
			},
		},
	},
}

func newOTTLMetrics(tb testing.TB, next consumer.Metrics) processor.Metrics {
	tb.Helper()
	factory := transformprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(containerMetricsOTTL).Unmarshal(cfg); err != nil {
		tb.Fatalf("unmarshal the OTTL block: %v", err)
	}
	proc, err := factory.CreateMetrics(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, next)
	if err != nil {
		tb.Fatalf("create transformprocessor: %v", err)
	}
	return proc
}

func newContainerNameMetrics(tb testing.TB, next consumer.Metrics) processor.Metrics {
	tb.Helper()
	f := containernameprocessor.NewFactory()
	p, err := f.CreateMetrics(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		tb.Fatalf("create containernameprocessor: %v", err)
	}
	return p
}

// containerMetrics returns one resource per entry, each holding one gauge point.
func containerMetrics(tb testing.TB, resources []map[string]any) pmetric.Metrics {
	tb.Helper()
	md := pmetric.NewMetrics()
	for _, attrs := range resources {
		rm := md.ResourceMetrics().AppendEmpty()
		if err := rm.Resource().Attributes().FromRaw(attrs); err != nil {
			tb.Fatalf("set resource attributes: %v", err)
		}
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("container.memory.usage.total")
		m.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
	}
	return md
}

func TestContainerNameMetricsParity_ServiceNameMatchesOTTL(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
	}{
		{"name and service", map[string]any{"container.name": "web-1", "service.name": "docker.service"}},
		{"name only", map[string]any{"container.name": "web-1"}},
		{"service only", map[string]any{"service.name": "docker.service"}},
		{"empty name and service", map[string]any{"container.name": "", "service.name": "docker.service"}},
		{"neither", map[string]any{"host.name": "h1"}},
	}
	ottlSink, procSink := new(consumertest.MetricsSink), new(consumertest.MetricsSink)
	ottl, proc := newOTTLMetrics(t, ottlSink), newContainerNameMetrics(t, procSink)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ottl.ConsumeMetrics(context.Background(), containerMetrics(t, []map[string]any{tc.attrs})); err != nil {
				t.Fatalf("ottl consume: %v", err)
			}
			if err := proc.ConsumeMetrics(context.Background(), containerMetrics(t, []map[string]any{tc.attrs})); err != nil {
				t.Fatalf("containername consume: %v", err)
			}
			want := ottlSink.AllMetrics()[i].ResourceMetrics().At(0).Resource().Attributes().AsRaw()
			got := procSink.AllMetrics()[i].ResourceMetrics().At(0).Resource().Attributes().AsRaw()
			if len(got) != len(want) {
				t.Fatalf("attributes = %v, OTTL gave %v", got, want)
			}
			for k, w := range want {
				if got[k] != w {
					t.Errorf("%s = %v, OTTL gave %v", k, got[k], w)
				}
			}
		})
	}
}

// containerMetricsBatch holds batchSize resources: five containers, with every tenth lacking a container name.
func containerMetricsBatch(b *testing.B) pmetric.Metrics {
	b.Helper()
	resources := make([]map[string]any, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		attrs := map[string]any{"host.name": "host-1", "container.id": fmt.Sprintf("%064d", i)}
		if i%10 != 0 {
			attrs["container.name"] = fmt.Sprintf("ctr-%d", i%5)
		}
		resources = append(resources, attrs)
	}
	return containerMetrics(b, resources)
}

// runMetricsBench times proc over fresh copies of template; copying is outside the timer.
func runMetricsBench(b *testing.B, proc consumer.Metrics, template pmetric.Metrics) {
	b.Helper()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		md := pmetric.NewMetrics()
		template.CopyTo(md)
		b.StartTimer()
		if err := proc.ConsumeMetrics(ctx, md); err != nil {
			b.Fatalf("consume: %v", err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*batchSize), "ns/resource")
}

func BenchmarkContainerNameMetrics(b *testing.B) {
	template := containerMetricsBatch(b)
	nop := consumertest.NewNop()

	b.Run("impl=ottl", func(b *testing.B) {
		runMetricsBench(b, newOTTLMetrics(b, nop), template)
	})
	b.Run("impl=containername", func(b *testing.B) {
		runMetricsBench(b, newContainerNameMetrics(b, nop), template)
	})
}
