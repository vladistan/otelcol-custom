// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName       = "github.com/vladistan/otelcol-custom/processor/sourcetagprocessor"
	unsourcedMetric = "otelcol_sourcetag_records_unsourced"

	uncategorizedMetric = "otelcol_sourcetag_records_uncategorized"
	providerAttr        = "provider"
)

type telemetry struct {
	unsourced     metric.Int64Counter
	uncategorized metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	unsourced, err := meter.Int64Counter(unsourcedMetric,
		metric.WithDescription("Log records that left the processor with no log.source, per provider."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	uncategorized, err := meter.Int64Counter(uncategorizedMetric,
		metric.WithDescription("Log records that left the processor with no event.category, per provider."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	return &telemetry{unsourced: unsourced, uncategorized: uncategorized}, nil
}

// add takes one count per ConsumeLogs call so a batch costs one Add per counter.
func (tel *telemetry) add(ctx context.Context, provider string, unsourced, uncategorized int64) {
	attrs := metric.WithAttributes(attribute.String(providerAttr, provider))
	if unsourced > 0 {
		tel.unsourced.Add(ctx, unsourced, attrs)
	}
	if uncategorized > 0 {
		tel.uncategorized.Add(ctx, uncategorized, attrs)
	}
}
