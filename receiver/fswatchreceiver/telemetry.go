// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/fswatchreceiver"

type telemetry struct {
	emitted, excluded, vanished, overflows, rescans, capped, timeouts, scanSkipped, completed, dropped metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	tel := &telemetry{}
	counters := []struct {
		dst        *metric.Int64Counter
		name, desc string
		unit       string
	}{
		{&tel.emitted, "otelcol_fswatch_records_emitted", "File records passed on.", "{record}"},
		{&tel.excluded, "otelcol_fswatch_excluded", "Files left out by an exclude pattern.", "{file}"},
		{&tel.vanished, "otelcol_fswatch_vanished", "Files gone before they could be read.", "{file}"},
		{&tel.overflows, "otelcol_fswatch_overflows", "Directory watch buffer overflows.", "{event}"},
		{&tel.rescans, "otelcol_fswatch_rescans", "Tree rescans started after a loss or reconnect.", "{scan}"},
		{&tel.capped, "otelcol_fswatch_rescans_capped", "Rescans held back by max_rescans.", "{scan}"},
		{&tel.timeouts, "otelcol_fswatch_settle_timeouts", "Write-complete records sent at settle_timeout, without a final size.", "{file}"},
		{&tel.dropped, "otelcol_fswatch_writes_dropped", "Write-complete records given up after failed deliveries.", "{record}"},
		{&tel.completed, "otelcol_fswatch_writes_completed", "Write-complete records passed on.", "{record}"},
		{&tel.scanSkipped, "otelcol_fswatch_scan_skipped", "Entries a scan left out, by reason.", "{entry}"},
	}
	for _, c := range counters {
		counter, err := meter.Int64Counter(c.name, metric.WithDescription(c.desc), metric.WithUnit(c.unit))
		if err != nil {
			return nil, err
		}
		*c.dst = counter
	}
	return tel, nil
}

func add(ctx context.Context, c metric.Int64Counter, n int64, attrs ...attribute.KeyValue) {
	if n > 0 {
		c.Add(ctx, n, metric.WithAttributes(attrs...))
	}
}

func (t *telemetry) emit(ctx context.Context, catchup bool, n int64) {
	add(ctx, t.emitted, n, attribute.Bool("catchup", catchup))
}

func (t *telemetry) skipped(ctx context.Context, reason string, n int64) {
	add(ctx, t.scanSkipped, n, attribute.String("reason", reason))
}
