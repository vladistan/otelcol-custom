// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/cloudwatchmetricsreceiver"

// selfMetrics reports, for each account, region and tier, how many series
// discovery found against the limit, so a truncated tier shows in the metrics.
type selfMetrics struct {
	discovered metric.Int64Gauge
	limit      metric.Int64Gauge
	truncated  metric.Int64Gauge
}

// newSelfMetrics returns nil when a gauge cannot be made; the receiver still
// runs and the truncation WARN is still logged.
func newSelfMetrics(provider metric.MeterProvider, logger *zap.Logger) *selfMetrics {
	meter := provider.Meter(scopeName)
	discovered, err1 := meter.Int64Gauge("otelcol_aws_cloudwatch_series_discovered",
		metric.WithDescription("Series found by discovery for an account, region and tier."))
	limit, err2 := meter.Int64Gauge("otelcol_aws_cloudwatch_series_limit",
		metric.WithDescription("Series limit of the tier."))
	truncated, err3 := meter.Int64Gauge("otelcol_aws_cloudwatch_discovery_truncated",
		metric.WithDescription("1 when discovery found more series than the limit, else 0."))
	if err1 != nil || err2 != nil || err3 != nil {
		logger.Debug("Cannot create the discovery gauges")
		return nil
	}
	return &selfMetrics{discovered: discovered, limit: limit, truncated: truncated}
}

func (s *selfMetrics) record(ctx context.Context, p *pairState, found discovered) {
	if s == nil {
		return
	}
	opts := metric.WithAttributes(
		attribute.String("account", p.account.Name),
		attribute.String("region", p.region),
		attribute.String("tier", p.tierName),
	)
	s.discovered.Record(ctx, int64(found.total), opts)
	s.limit.Record(ctx, int64(p.tier.Limit), opts)
	flag := int64(0)
	if found.truncated() {
		flag = 1
	}
	s.truncated.Record(ctx, flag, opts)
}
