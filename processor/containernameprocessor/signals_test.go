// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"
)

const dockerStatsScope = "docker_stats"

func metricsOf(t *testing.T, resources ...map[string]any) pmetric.Metrics {
	t.Helper()
	md := pmetric.NewMetrics()
	for _, attrs := range resources {
		rm := md.ResourceMetrics().AppendEmpty()
		if err := rm.Resource().Attributes().FromRaw(attrs); err != nil {
			t.Fatalf("set resource attributes: %v", err)
		}
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName(dockerStatsScope)
		m := sm.Metrics().AppendEmpty()
		m.SetName("container.memory.usage.total")
		m.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(42)
	}
	return md
}

func tracesOf(t *testing.T, resources ...map[string]any) ptrace.Traces {
	t.Helper()
	td := ptrace.NewTraces()
	for _, attrs := range resources {
		rs := td.ResourceSpans().AppendEmpty()
		if err := rs.Resource().Attributes().FromRaw(attrs); err != nil {
			t.Fatalf("set resource attributes: %v", err)
		}
		rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("op")
	}
	return td
}

func runMetrics(t *testing.T, md pmetric.Metrics) pmetric.Metrics {
	t.Helper()
	f := NewFactory()
	sink := new(consumertest.MetricsSink)
	p, err := f.CreateMetrics(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create metrics processor: %v", err)
	}
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllMetrics()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllMetrics()))
	}
	return sink.AllMetrics()[0]
}

func runTraces(t *testing.T, td ptrace.Traces) ptrace.Traces {
	t.Helper()
	f := NewFactory()
	sink := new(consumertest.TracesSink)
	p, err := f.CreateTraces(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), sink)
	if err != nil {
		t.Fatalf("create traces processor: %v", err)
	}
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllTraces()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllTraces()))
	}
	return sink.AllTraces()[0]
}

// serviceCases holds the rule shared by the three signals.
var serviceCases = []struct {
	name string
	in   map[string]any
	want map[string]any
}{
	{
		name: "an existing service name is overwritten",
		in:   map[string]any{"container.name": webName, "service.name": journaldUnit},
		want: map[string]any{"container.name": webName, "service.name": webName},
	},
	{
		name: "a missing service name is set",
		in:   map[string]any{"container.name": webName},
		want: map[string]any{"container.name": webName, "service.name": webName},
	},
	{
		name: "no container name leaves the resource alone",
		in:   map[string]any{"service.name": journaldUnit},
		want: map[string]any{"service.name": journaldUnit},
	},
	{
		name: "no container name and no service name stays without one",
		in:   map[string]any{"host.name": "h1"},
		want: map[string]any{"host.name": "h1"},
	},
	{
		name: "an empty container name leaves the resource alone",
		in:   map[string]any{"container.name": "", "service.name": journaldUnit},
		want: map[string]any{"container.name": "", "service.name": journaldUnit},
	},
	{
		name: "a container name that is not a string leaves the resource alone",
		in:   map[string]any{"container.name": int64(7), "service.name": journaldUnit},
		want: map[string]any{"container.name": int64(7), "service.name": journaldUnit},
	},
	{
		name: "other resource attributes are kept",
		in:   map[string]any{"container.name": webName, "container.id": "abc", "host.name": "h1"},
		want: map[string]any{"container.name": webName, "container.id": "abc", "host.name": "h1", "service.name": webName},
	},
}

func sameAttrs(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("attributes = %v, want %v", got, want)
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s = %v, want %v", key, got[key], w)
		}
	}
}

func TestConsumeMetrics_ServiceNameFollowsContainerName(t *testing.T) {
	for _, tc := range serviceCases {
		t.Run(tc.name, func(t *testing.T) {
			out := runMetrics(t, metricsOf(t, tc.in))
			sameAttrs(t, out.ResourceMetrics().At(0).Resource().Attributes().AsRaw(), tc.want)
		})
	}
}

func TestConsumeTraces_ServiceNameFollowsContainerName(t *testing.T) {
	for _, tc := range serviceCases {
		t.Run(tc.name, func(t *testing.T) {
			out := runTraces(t, tracesOf(t, tc.in))
			sameAttrs(t, out.ResourceSpans().At(0).Resource().Attributes().AsRaw(), tc.want)
		})
	}
}

func TestConsumeMetrics_EachResourceUsesItsOwnContainerName(t *testing.T) {
	out := runMetrics(t, metricsOf(t,
		map[string]any{"container.name": "web-1"},
		map[string]any{"service.name": "other"},
		map[string]any{"container.name": "db-1", "service.name": "old"},
	))

	for i, want := range []any{"web-1", "other", "db-1"} {
		if got := out.ResourceMetrics().At(i).Resource().Attributes().AsRaw()["service.name"]; got != want {
			t.Errorf("resource %d service.name = %v, want %v", i, got, want)
		}
	}
}

func TestConsumeMetrics_ContainerNameOnADataPointIsIgnored(t *testing.T) {
	md := metricsOf(t, map[string]any{"service.name": "other"})
	dp := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	dp.Attributes().PutStr("container.name", webName)

	out := runMetrics(t, md)

	if got := out.ResourceMetrics().At(0).Resource().Attributes().AsRaw()["service.name"]; got != "other" {
		t.Errorf("service.name = %v, want other", got)
	}
}

func TestConsumeMetrics_PointsPassThrough(t *testing.T) {
	out := runMetrics(t, metricsOf(t, map[string]any{"container.name": webName}))

	if out.DataPointCount() != 1 {
		t.Fatalf("data point count = %d, want 1", out.DataPointCount())
	}
	m := out.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	if m.Name() != "container.memory.usage.total" || m.Gauge().DataPoints().At(0).IntValue() != 42 {
		t.Errorf("metric changed: %s %d", m.Name(), m.Gauge().DataPoints().At(0).IntValue())
	}
}

func TestConsumeMetrics_SecondPassChangesNothing(t *testing.T) {
	once := runMetrics(t, metricsOf(t, map[string]any{"container.name": webName, "service.name": "x"}))
	want := once.ResourceMetrics().At(0).Resource().Attributes().AsRaw()

	got := runMetrics(t, once).ResourceMetrics().At(0).Resource().Attributes().AsRaw()

	sameAttrs(t, got, want)
}

func TestConsumeMetrics_EmptyBatchPassesThrough(t *testing.T) {
	if out := runMetrics(t, pmetric.NewMetrics()); out.ResourceMetrics().Len() != 0 {
		t.Errorf("resources = %d, want 0", out.ResourceMetrics().Len())
	}
}

type failingSignals struct{ err error }

func (failingSignals) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (f failingSignals) ConsumeMetrics(context.Context, pmetric.Metrics) error { return f.err }

func (f failingSignals) ConsumeTraces(context.Context, ptrace.Traces) error { return f.err }

func TestConsumeMetrics_ReturnsTheNextConsumersError(t *testing.T) {
	want := errors.New("downstream refused")
	f := NewFactory()
	p, err := f.CreateMetrics(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), failingSignals{err: want})
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}

	if got := p.ConsumeMetrics(context.Background(), metricsOf(t, map[string]any{"container.name": webName})); !errors.Is(got, want) {
		t.Errorf("error = %v, want %v", got, want)
	}
}

func TestConsumeTraces_ReturnsTheNextConsumersError(t *testing.T) {
	want := errors.New("downstream refused")
	f := NewFactory()
	p, err := f.CreateTraces(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), failingSignals{err: want})
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}

	if got := p.ConsumeTraces(context.Background(), tracesOf(t, map[string]any{"container.name": webName})); !errors.Is(got, want) {
		t.Errorf("error = %v, want %v", got, want)
	}
}

func TestLifecycle_MetricsAndTracesProcessors(t *testing.T) {
	f := NewFactory()
	set := processortest.NewNopSettings(f.Type())
	m, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	if err != nil {
		t.Fatalf("create metrics processor: %v", err)
	}
	tr, err := f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	if err != nil {
		t.Fatalf("create traces processor: %v", err)
	}

	for name, p := range map[string]interface {
		Start(context.Context, component.Host) error
		Shutdown(context.Context) error
		Capabilities() consumer.Capabilities
	}{"metrics": m, "traces": tr} {
		if err := p.Start(context.Background(), nil); err != nil {
			t.Errorf("%s start: %v", name, err)
		}
		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("%s shutdown: %v", name, err)
		}
		if !p.Capabilities().MutatesData {
			t.Errorf("%s Capabilities().MutatesData = false, want true", name)
		}
	}
}
