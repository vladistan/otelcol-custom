// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeName    = "github.com/vladistan/otelcol-custom/processor/identityfillprocessor"
	filledMetric = "otelcol_identityfill_records_filled"

	ruleAttribute   = "rule"
	ruleHostName    = "host_name"
	ruleServiceName = "service_name"
	ruleAuditBody   = "audit_body"
)

type telemetry struct {
	filled metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	filled, err := ts.MeterProvider.Meter(scopeName).Int64Counter(filledMetric,
		metric.WithDescription("Log records given a value by a fill rule, per rule."),
		metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	return &telemetry{filled: filled}, nil
}

// tally batches one ConsumeLogs call so each rule costs one Add.
type tally struct {
	host, service, body int64
}

func (tel *telemetry) flush(ctx context.Context, t tally) {
	tel.add(ctx, ruleHostName, t.host)
	tel.add(ctx, ruleServiceName, t.service)
	tel.add(ctx, ruleAuditBody, t.body)
}

func (tel *telemetry) add(ctx context.Context, rule string, n int64) {
	if n > 0 {
		tel.filled.Add(ctx, n, metric.WithAttributes(attribute.String(ruleAttribute, rule)))
	}
}
