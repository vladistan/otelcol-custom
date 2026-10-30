// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Self-metric names.
const (
	metricSeriesActive     = "otelcol_processor_windowhist_series_active"
	metricSamplesReceived  = "otelcol_processor_windowhist_samples_received"
	metricSamplesDiscarded = "otelcol_processor_windowhist_samples_discarded"
)

// Reasons a sample is discarded.
const (
	reasonInvalid      = "invalid"
	reasonLate         = "late"
	reasonCounterReset = "counter_reset"
)

type telemetry struct {
	received  metric.Int64Counter
	discarded metric.Int64Counter
}

// newTelemetry registers the three self-metrics; active reports the live series count.
func newTelemetry(meter metric.Meter, active func() int64) (*telemetry, error) {
	received, err := meter.Int64Counter(metricSamplesReceived,
		metric.WithDescription("Data points of grouped metrics received."))
	if err != nil {
		return nil, err
	}
	discarded, err := meter.Int64Counter(metricSamplesDiscarded,
		metric.WithDescription("Data points dropped, by reason."))
	if err != nil {
		return nil, err
	}
	_, err = meter.Int64ObservableGauge(metricSeriesActive,
		metric.WithDescription("Series currently buffered."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(active())
			return nil
		}))
	if err != nil {
		return nil, err
	}
	return &telemetry{received: received, discarded: discarded}, nil
}

func (t *telemetry) receive() {
	t.received.Add(context.Background(), 1)
}

func (t *telemetry) discard(reason string) {
	t.discarded.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
}
