// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// measured runs each batch through a processor with a metric reader attached
// and returns the histogram points the processor recorded.
func measured(t *testing.T, cfg *Config, batches ...plog.Logs) metricdata.HistogramDataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, ld := range batches {
		if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != resourcesPerBatchMetric {
				continue
			}
			if m.Unit != "{resource}" {
				t.Errorf("unit = %q, want {resource}", m.Unit)
			}
			h := m.Data.(metricdata.Histogram[int64])
			if len(h.DataPoints) != 1 {
				t.Fatalf("got %d data points, want 1", len(h.DataPoints))
			}
			return h.DataPoints[0]
		}
	}
	t.Fatalf("metric %s not recorded", resourcesPerBatchMetric)
	return metricdata.HistogramDataPoint[int64]{}
}

func threeExeBatch(t *testing.T) plog.Logs {
	t.Helper()
	a, b, c := fullRecord(), fullRecord(), fullRecord()
	a["_EXE"], b["_EXE"], c["_EXE"] = "/usr/bin/a", "/usr/bin/b", "/usr/bin/c"
	return batchWith(t, map[string]any{}, a, b, c)
}

func TestSelfMetric_ThreeDistinctExeValuesRecordThreeResources(t *testing.T) {
	point := measured(t, extendedConfig(), threeExeBatch(t))

	if point.Count != 1 || point.Sum != 3 {
		t.Errorf("count %d sum %d, want one batch with 3 resources", point.Count, point.Sum)
	}
}

func TestSelfMetric_EveryBatchIsRecorded(t *testing.T) {
	one := batchWith(t, map[string]any{}, fullRecord())

	point := measured(t, extendedConfig(), one, threeExeBatch(t))

	if point.Count != 2 || point.Sum != 4 {
		t.Errorf("count %d sum %d, want two batches with 4 resources in all", point.Count, point.Sum)
	}
}

func TestSelfMetric_UnsplitBatchRecordsItsOneResource(t *testing.T) {
	cfg := extendedConfig()
	cfg.SplitResources = false

	point := measured(t, cfg, threeExeBatch(t))

	if point.Count != 1 || point.Sum != 1 {
		t.Errorf("count %d sum %d, want one batch with 1 resource", point.Count, point.Sum)
	}
}

func TestSelfMetric_RecordedWithTheExtendedMappingOffToo(t *testing.T) {
	point := measured(t, defaultConfig(), threeExeBatch(t))

	if point.Count != 1 || point.Sum != 1 {
		t.Errorf("count %d sum %d, want one batch with 1 resource (no split on _EXE when off)", point.Count, point.Sum)
	}
}

func TestSelfMetric_EmptyBatchRecordsZero(t *testing.T) {
	point := measured(t, extendedConfig(), plog.NewLogs())

	if point.Count != 1 || point.Sum != 0 {
		t.Errorf("count %d sum %d, want one batch with 0 resources", point.Count, point.Sum)
	}
}

func TestSelfMetric_NoCapOnTheResourceCount(t *testing.T) {
	records := make([]map[string]any, 0, 300)
	for i := 0; i < 300; i++ {
		r := fullRecord()
		r["_EXE"] = "/usr/bin/p" + string(rune('A'+i%26)) + string(rune('a'+i/26%26)) + string(rune('0'+i/676))
		records = append(records, r)
	}

	point := measured(t, extendedConfig(), batchWith(t, map[string]any{}, records...))

	if point.Sum < 300 {
		t.Errorf("sum %d, want at least 300 resources: nothing caps the split", point.Sum)
	}
}

// failingMeterProvider hands out a meter whose histogram cannot be created.
type failingMeterProvider struct{ noop.MeterProvider }

func (failingMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter { return failingMeter{} }

type failingMeter struct{ noop.Meter }

func (failingMeter) Int64Histogram(string, ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	return nil, errors.New("no histogram")
}

func TestSelfMetric_CreateFailsWhenTheInstrumentCannotBeMade(t *testing.T) {
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = failingMeterProvider{}

	if _, err := NewFactory().CreateLogs(context.Background(), set, defaultConfig(), new(consumertest.LogsSink)); err == nil {
		t.Error("create succeeded without a histogram")
	}
}
