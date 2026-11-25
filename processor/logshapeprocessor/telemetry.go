// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName         = "github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
	unmatchedMetric   = "otelcol_logshape_unmatched"
	parseFailedMetric = "otelcol_logshape_parse_failed"
	sampledOutMetric  = "otelcol_logshape_sampled_out"
	sourceAttr        = "source"
	formatAttr        = "format"
)

type telemetry struct {
	unmatched   metric.Int64Counter
	parseFailed metric.Int64Counter
	sampledOut  metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	unmatched, err := meter.Int64Counter(unmatchedMetric,
		metric.WithDescription("Log records no parser claimed, per log.source."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	parseFailed, err := meter.Int64Counter(parseFailedMetric,
		metric.WithDescription("Log records a parser claimed but could not parse, per format."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	sampledOut, err := meter.Int64Counter(sampledOutMetric,
		metric.WithDescription("Log records a parser sampled out and dropped, per format."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	return &telemetry{unmatched: unmatched, parseFailed: parseFailed, sampledOut: sampledOut}, nil
}

// flush adds the counts of one batch with one Add per label value.
func (t *telemetry) flush(ctx context.Context, unmatched, parseFailed, sampledOut map[string]int64) {
	for source, n := range unmatched {
		t.unmatched.Add(ctx, n, metric.WithAttributes(attribute.String(sourceAttr, source)))
	}
	for format, n := range parseFailed {
		t.parseFailed.Add(ctx, n, metric.WithAttributes(attribute.String(formatAttr, format)))
	}
	for format, n := range sampledOut {
		t.sampledOut.Add(ctx, n, metric.WithAttributes(attribute.String(formatAttr, format)))
	}
}
