// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/pvereceiver"

// metricBuilder appends data points to one metric per name in one resource.
type metricBuilder struct {
	md      pmetric.Metrics
	sm      pmetric.ScopeMetrics
	metrics map[string]pmetric.Metric
	now     pcommon.Timestamp
}

func newMetricBuilder(md pmetric.Metrics, now pcommon.Timestamp) *metricBuilder {
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	return &metricBuilder{md: md, sm: sm, metrics: map[string]pmetric.Metric{}, now: now}
}

func (b *metricBuilder) metric(name, unit string, sum bool) pmetric.Metric {
	if m, ok := b.metrics[name]; ok {
		return m
	}
	m := b.sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	if sum {
		s := m.SetEmptySum()
		s.SetIsMonotonic(true)
		s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	} else {
		m.SetEmptyGauge()
	}
	b.metrics[name] = m
	return m
}

func putAttrs(dp pmetric.NumberDataPoint, kv []string) {
	for i := 0; i+1 < len(kv); i += 2 {
		dp.Attributes().PutStr(kv[i], kv[i+1])
	}
}

// gauge appends one gauge point; kv is a flat list of attribute keys and values.
func (b *metricBuilder) gauge(name, unit string, value float64, kv ...string) {
	dp := b.metric(name, unit, false).Gauge().DataPoints().AppendEmpty()
	dp.SetTimestamp(b.now)
	dp.SetDoubleValue(value)
	putAttrs(dp, kv)
}

// counter appends one cumulative monotonic sum point that started at start.
func (b *metricBuilder) counter(name, unit string, value float64, start pcommon.Timestamp, kv ...string) {
	dp := b.metric(name, unit, true).Sum().DataPoints().AppendEmpty()
	dp.SetStartTimestamp(start)
	dp.SetTimestamp(b.now)
	dp.SetDoubleValue(value)
	putAttrs(dp, kv)
}
