// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type input struct {
	body     string
	attrs    map[string]any
	resource map[string]any
	scope    string
	ts       time.Time

	// bodyMap, when set, is the body in place of the text: a journald record as the receiver makes it.
	bodyMap map[string]any
	// severity and severityText are the severity the record arrives with.
	severity     plog.SeverityNumber
	severityText string
}

func (in input) logs() plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(in.resource); err != nil {
		panic(err)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName(in.scope)
	lr := sl.LogRecords().AppendEmpty()
	lr.Body().SetStr(in.body)
	if in.bodyMap != nil {
		if err := lr.Body().SetEmptyMap().FromRaw(in.bodyMap); err != nil {
			panic(err)
		}
	}
	lr.SetSeverityNumber(in.severity)
	lr.SetSeverityText(in.severityText)
	if err := lr.Attributes().FromRaw(in.attrs); err != nil {
		panic(err)
	}
	if !in.ts.IsZero() {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(in.ts))
	}
	return ld
}

type harness struct {
	t      *testing.T
	proc   *shapeProcessor
	sink   *consumertest.LogsSink
	reader *sdkmetric.ManualReader
}

func newHarness(t *testing.T, cfg *Config) *harness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sink := new(consumertest.LogsSink)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	proc, err := newProcessor(set, cfg, sink)
	if err != nil {
		t.Fatalf("newProcessor: %v", err)
	}
	return &harness{t: t, proc: proc, sink: sink, reader: reader}
}

// run sends one record and returns it as it left the processor.
func (h *harness) run(in input) (plog.LogRecord, pcommon.Resource) {
	h.t.Helper()
	if err := h.proc.ConsumeLogs(context.Background(), in.logs()); err != nil {
		h.t.Fatalf("ConsumeLogs: %v", err)
	}
	all := h.sink.AllLogs()
	if len(all) == 0 {
		h.t.Fatal("nothing reached the next consumer")
	}
	rl := all[len(all)-1].ResourceLogs().At(0)
	return rl.ScopeLogs().At(0).LogRecords().At(0), rl.Resource()
}

func (h *harness) counter(name, attrKey string) map[string]int64 {
	h.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		h.t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(attrKey))
				got[v.AsString()] += dp.Value
			}
		}
	}
	return got
}

func defaultConfig() *Config { return createDefaultConfig().(*Config) }

func attrStr(t *testing.T, m pcommon.Map, key string) string {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("%s missing; have %v", key, m.AsRaw())
	}
	if v.Type() != pcommon.ValueTypeStr {
		t.Fatalf("%s is %s, want a string", key, v.Type())
	}
	return v.Str()
}

func attrInt(t *testing.T, m pcommon.Map, key string, want int64) {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("%s missing; have %v", key, m.AsRaw())
	}
	if v.Type() != pcommon.ValueTypeInt || v.Int() != want {
		t.Fatalf("%s = %v (%s), want int %d", key, v.AsRaw(), v.Type(), want)
	}
}

func absent(t *testing.T, m pcommon.Map, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m.Get(k); ok {
			t.Fatalf("%s present: %v", k, m.AsRaw())
		}
	}
}
