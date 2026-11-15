// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName      = "github.com/vladistan/otelcol-custom/processor/tightvncprocessor"
	keptMetric     = "otelcol_tightvnc_records_kept"
	droppedMetric  = "otelcol_tightvnc_records_dropped"
	unparsedMetric = "otelcol_tightvnc_records_unparsed"
	recordsUnit    = "{record}"
)

type telemetry struct {
	kept, dropped, unparsed metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	kept, err := meter.Int64Counter(keptMetric,
		metric.WithDescription("Parsed log records passed on."), metric.WithUnit(recordsUnit))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter(droppedMetric,
		metric.WithDescription("Unmarked noise records removed."), metric.WithUnit(recordsUnit))
	if err != nil {
		return nil, err
	}
	unparsed, err := meter.Int64Counter(unparsedMetric,
		metric.WithDescription("Records outside the line grammar, passed on unchanged."), metric.WithUnit(recordsUnit))
	if err != nil {
		return nil, err
	}
	return &telemetry{kept: kept, dropped: dropped, unparsed: unparsed}, nil
}

// tally batches one ConsumeLogs call so each counter costs one Add.
type tally struct {
	kept, dropped, unparsed int64
}

func (tel *telemetry) flush(ctx context.Context, t tally) {
	add(ctx, tel.kept, t.kept)
	add(ctx, tel.dropped, t.dropped)
	add(ctx, tel.unparsed, t.unparsed)
}

func add(ctx context.Context, counter metric.Int64Counter, n int64) {
	if n > 0 {
		counter.Add(ctx, n)
	}
}
