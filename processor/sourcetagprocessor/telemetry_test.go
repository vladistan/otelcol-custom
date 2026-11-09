// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"
	"maps"
	"testing"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func counterByProvider(t *testing.T, reader *sdkmetric.ManualReader, name string) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				provider, _ := dp.Attributes.Value("provider")
				got[provider.AsString()] += dp.Value
			}
		}
	}
	return got
}

func TestSelfMetrics_CountRecordsLeavingWithoutASource(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := nopSettings()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	proc := newProc(t, "aws", new(consumertest.LogsSink), set)
	ld := plog.NewLogs()
	for _, in := range []input{{scope: cloudTrail}, {}, {attrs: map[string]string{"x": "y"}}} {
		in.logs().ResourceLogs().MoveAndAppendTo(ld.ResourceLogs())
	}

	consume(t, proc, ld)

	if want := map[string]int64{"aws": 2}; !maps.Equal(counterByProvider(t, reader, unsourcedMetric), want) {
		t.Errorf("unsourced records = %v, want %v", counterByProvider(t, reader, unsourcedMetric), want)
	}
}

func TestSelfMetrics_NothingIsRecordedWhenEveryRecordHasASource(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := nopSettings()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	proc := newProc(t, "azure", new(consumertest.LogsSink), set)

	consume(t, proc, input{}.logs())

	if got := counterByProvider(t, reader, unsourcedMetric); len(got) != 0 {
		t.Errorf("unsourced records = %v, want none", got)
	}
}

func TestSelfMetrics_CountRecordsLeavingWithoutACategory(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := nopSettings()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	proc := newProc(t, "aws", new(consumertest.LogsSink), set)

	consume(t, proc, input{attrs: map[string]string{"log.source": "custom"}}.logs())

	if want := map[string]int64{"aws": 1}; !maps.Equal(counterByProvider(t, reader, uncategorizedMetric), want) {
		t.Errorf("uncategorized records = %v, want %v", counterByProvider(t, reader, uncategorizedMetric), want)
	}
	if got := counterByProvider(t, reader, unsourcedMetric); len(got) != 0 {
		t.Errorf("unsourced records = %v, want none", got)
	}
}

func TestSelfMetrics_FullyTaggedRecordsCountNowhere(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := nopSettings()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	proc := newProc(t, "azure", new(consumertest.LogsSink), set)

	consume(t, proc, input{}.logs())

	for _, name := range []string{unsourcedMetric, uncategorizedMetric} {
		if got := counterByProvider(t, reader, name); len(got) != 0 {
			t.Errorf("%s = %v, want none", name, got)
		}
	}
}
