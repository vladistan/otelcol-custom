// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/exporter/flowlogexporter"
	reasonKey = "reason"
)

// The reasons of dropped rows.
const (
	dropQueueFull    = "queue_full"
	dropInsertFailed = "insert_failed"
	dropSchema       = "schema"
	dropInvalid      = "invalid"
)

const warnEmptyPassword = "empty_password" // pragma: allowlist secret

type telemetry struct {
	inserted        metric.Int64Counter
	dropped         metric.Int64Counter
	configWarnings  metric.Int64Counter
	mappingWarnings metric.Int64Counter
	unknownExporter metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	t := &telemetry{}
	for _, c := range []struct {
		dst  *metric.Int64Counter
		name string
		desc string
	}{
		{&t.inserted, "otelcol_flowlog_rows_inserted", "Rows inserted into the flow table."},
		{&t.dropped, "otelcol_flowlog_rows_dropped", "Rows dropped, per reason."},
		{&t.configWarnings, "otelcol_flowlog_config_warnings", "Configuration warnings at start, per reason."},
		{&t.mappingWarnings, "otelcol_flowlog_mapping_warnings", "Records mapped with a default, per reason."},
		{&t.unknownExporter, "otelcol_flowlog_unknown_exporter", "Records from an exporter address that is not in routers."},
	} {
		counter, err := meter.Int64Counter(c.name, metric.WithDescription(c.desc), metric.WithUnit("{row}"))
		if err != nil {
			return nil, err
		}
		*c.dst = counter
	}
	return t, nil
}

func (t *telemetry) drop(ctx context.Context, rows int64, reason string) {
	t.dropped.Add(ctx, rows, metric.WithAttributes(attribute.String(reasonKey, reason)))
}

func (t *telemetry) mappingWarning(ctx context.Context, reason string) {
	t.mappingWarnings.Add(ctx, 1, metric.WithAttributes(attribute.String(reasonKey, reason)))
}

func (t *telemetry) configWarning(ctx context.Context, reason string) {
	t.configWarnings.Add(ctx, 1, metric.WithAttributes(attribute.String(reasonKey, reason)))
}
