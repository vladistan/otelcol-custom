// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type harness struct {
	proc   processor.Logs
	sink   *consumertest.LogsSink
	reader *sdkmetric.ManualReader
}

func newHarness(t *testing.T, rules []DropRule) *harness {
	t.Helper()
	return newHarnessCfg(t, &Config{DropRules: rules})
}

func newHarnessCfg(t *testing.T, cfg *Config) *harness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = provider
	sink := new(consumertest.LogsSink)
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	return &harness{proc: proc, sink: sink, reader: reader}
}

func (h *harness) counter(t *testing.T, name string, attrs ...attribute.KeyValue) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := attribute.NewSet(attrs...)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want Sum[int64]", name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				if dp.Attributes.Equals(&want) {
					return dp.Value
				}
			}
		}
	}
	return 0
}

// droppedRule returns the rule label of the dropped counter's first data point.
func (h *harness) droppedRule(t *testing.T) (string, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, scopeMetrics := range rm.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Name != "otelcol_winlog_events_dropped" {
				continue
			}
			for _, dp := range sum.DataPoints {
				if name, found := dp.Attributes.Value("rule"); found && dp.Value > 0 {
					return name.AsString(), true
				}
			}
		}
	}
	return "", false
}

func batch(events ...testEvent) plog.Logs {
	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, e := range events {
		e.toLogRecord().CopyTo(records.AppendEmpty())
	}
	return logs
}

func repeat(e testEvent, n int) []testEvent {
	out := make([]testEvent, n)
	for i := range out {
		out[i] = e
	}
	return out
}

func channelAttr(v string) attribute.KeyValue { return attribute.String("channel", v) }
func ruleAttr(v string) attribute.KeyValue    { return attribute.String("rule", v) }

func TestDroppedCounterRisesByNAndReadByAtLeastN(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	const n = 7
	events := append(repeat(testEvent{channel: "Security", id: 4624}, n),
		testEvent{channel: "Security", id: 4625}, testEvent{channel: "System", id: 7036})
	if err := h.proc.ConsumeLogs(context.Background(), batch(events...)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.counter(t, "otelcol_winlog_events_dropped", channelAttr("Security"), ruleAttr("security-audit-noise")); got != n {
		t.Errorf("dropped{Security, security-audit-noise} = %d, want %d", got, n)
	}
	if got := h.counter(t, "otelcol_winlog_events_read", channelAttr("Security")); got != n+1 {
		t.Errorf("read{Security} = %d, want %d", got, n+1)
	}
	if got := h.counter(t, "otelcol_winlog_events_read", channelAttr("System")); got != 1 {
		t.Errorf("read{System} = %d, want 1", got)
	}
	if got := h.sink.LogRecordCount(); got != 2 {
		t.Errorf("forwarded %d records, want 2 (4625 and 7036)", got)
	}
}

func TestCountersAccumulateAcrossBatches(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	for i := 0; i < 3; i++ {
		if err := h.proc.ConsumeLogs(context.Background(), batch(testEvent{channel: "Security", id: 4624})); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	if got := h.counter(t, "otelcol_winlog_events_dropped", channelAttr("Security"), ruleAttr("security-audit-noise")); got != 3 {
		t.Errorf("dropped after 3 batches = %d, want 3", got)
	}
}

func TestDroppedCounterNamesFirstMatchingRule(t *testing.T) {
	h := newHarness(t, []DropRule{
		{Name: "first", EventIDs: []string{"100"}},
		{Name: "second", EventIDs: []string{"100"}},
	})
	if err := h.proc.ConsumeLogs(context.Background(), batch(testEvent{channel: "App", id: 100})); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.counter(t, "otelcol_winlog_events_dropped", channelAttr("App"), ruleAttr("first")); got != 1 {
		t.Errorf("dropped{first} = %d, want 1", got)
	}
	if got := h.counter(t, "otelcol_winlog_events_dropped", channelAttr("App"), ruleAttr("second")); got != 0 {
		t.Errorf("dropped{second} = %d, want 0", got)
	}
}

func TestFullyDroppedBatchIsNotForwarded(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	if err := h.proc.ConsumeLogs(context.Background(), batch(repeat(testEvent{channel: "Security", id: 4624}, 3)...)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := len(h.sink.AllLogs()); got != 0 {
		t.Errorf("sink received %d batches, want 0", got)
	}
}

func TestNoRulesPassesEverythingAndStillCountsReads(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.proc.ConsumeLogs(context.Background(), batch(repeat(testEvent{channel: "Security", id: 4624}, 4)...)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 4 {
		t.Errorf("forwarded %d records, want 4", got)
	}
	if got := h.counter(t, "otelcol_winlog_events_read", channelAttr("Security")); got != 4 {
		t.Errorf("read{Security} = %d, want 4", got)
	}
}

func TestRecordWithoutMapBodyPassesUntouchedAndUncounted(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("plain")
	if err := h.proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 1 {
		t.Fatalf("forwarded %d records, want 1", got)
	}
	if body := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str(); body != "plain" {
		t.Errorf("body = %q, want plain", body)
	}
}

func TestRecordWithMapBodyButNoChannelPassesUncounted(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetEmptyMap().PutStr("message", "x")
	if err := h.proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 1 {
		t.Errorf("forwarded %d records, want 1", got)
	}
	if got := h.counter(t, "otelcol_winlog_events_read", channelAttr("")); got != 0 {
		t.Errorf("read{} = %d, want 0", got)
	}
}

func TestEmptyResourcesAreRemovedAfterDrops(t *testing.T) {
	h := newHarness(t, DefaultDropRules())
	logs := plog.NewLogs()
	for _, e := range []testEvent{{channel: "Security", id: 4624}, {channel: "System", id: 7036}} {
		e.toLogRecord().CopyTo(logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty())
	}
	if err := h.proc.ConsumeLogs(context.Background(), logs); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := h.sink.AllLogs()[0].ResourceLogs().Len(); got != 1 {
		t.Errorf("forwarded %d resources, want 1", got)
	}
}
