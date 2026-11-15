// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package processlifetimeprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	uptimeMetric = "process.uptime"
	cpuMetric    = "process.cpu.time"
	ioMetric     = "process.disk.io"

	cpuRatioMetric = "process.cpu.lifetime.utilization"
	ioRatioMetric  = "process.disk.io.lifetime.rate"
)

type processorImpl struct {
	next consumer.Metrics
}

func newProcessor(next consumer.Metrics) *processorImpl { return &processorImpl{next: next} }

func (*processorImpl) Start(context.Context, component.Host) error { return nil }

func (*processorImpl) Shutdown(context.Context) error { return nil }

func (*processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeMetrics adds the two ratio gauges to each resource that reports a
// positive process.uptime. Existing metrics are not touched.
func (p *processorImpl) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		addRatios(md.ResourceMetrics().At(i))
	}
	return p.next.ConsumeMetrics(ctx, md)
}

// addRatios appends each ratio to the scope that holds its source metric.
func addRatios(rm pmetric.ResourceMetrics) {
	uptime, ok := uptimeSeconds(rm)
	if !ok {
		return
	}
	if !hasMetric(rm, cpuRatioMetric) {
		if src, scope, found := cumulativeSum(rm, cpuMetric); found {
			addCPURatio(scope, src, uptime)
		}
	}
	if !hasMetric(rm, ioRatioMetric) {
		if src, scope, found := cumulativeSum(rm, ioMetric); found {
			addIORatio(scope, src, uptime)
		}
	}
}

// uptimeSeconds reads the first point of a process.uptime gauge; zero, a
// negative value and any other metric type are unusable as a divisor.
func uptimeSeconds(rm pmetric.ResourceMetrics) (float64, bool) {
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		ms := rm.ScopeMetrics().At(i).Metrics()
		for j := 0; j < ms.Len(); j++ {
			m := ms.At(j)
			if m.Name() != uptimeMetric || m.Type() != pmetric.MetricTypeGauge || m.Gauge().DataPoints().Len() == 0 {
				continue
			}
			v := pointValue(m.Gauge().DataPoints().At(0))
			return v, v > 0
		}
	}
	return 0, false
}

func hasMetric(rm pmetric.ResourceMetrics, name string) bool {
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		ms := rm.ScopeMetrics().At(i).Metrics()
		for j := 0; j < ms.Len(); j++ {
			if ms.At(j).Name() == name {
				return true
			}
		}
	}
	return false
}

// cumulativeSum finds a cumulative sum with at least one point; a delta sum
// is not a lifetime total.
func cumulativeSum(rm pmetric.ResourceMetrics, name string) (pmetric.Metric, pmetric.ScopeMetrics, bool) {
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		sm := rm.ScopeMetrics().At(i)
		for j := 0; j < sm.Metrics().Len(); j++ {
			m := sm.Metrics().At(j)
			if m.Name() == name && m.Type() == pmetric.MetricTypeSum &&
				m.Sum().AggregationTemporality() == pmetric.AggregationTemporalityCumulative &&
				m.Sum().DataPoints().Len() > 0 {
				return m, sm, true
			}
		}
	}
	return pmetric.Metric{}, pmetric.ScopeMetrics{}, false
}

func pointValue(dp pmetric.NumberDataPoint) float64 {
	if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
		return float64(dp.IntValue())
	}
	return dp.DoubleValue()
}

// addCPURatio writes one point: CPU seconds over every state, per second of age.
func addCPURatio(scope pmetric.ScopeMetrics, src pmetric.Metric, uptime float64) {
	var total float64
	var at pcommon.Timestamp
	dps := src.Sum().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		total += pointValue(dps.At(i))
		at = max(at, dps.At(i).Timestamp())
	}
	m := scope.Metrics().AppendEmpty()
	m.SetName(cpuRatioMetric)
	m.SetDescription("CPU time over all states divided by the age of the process")
	m.SetUnit("1")
	dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
	dp.SetDoubleValue(total / uptime)
	dp.SetTimestamp(at)
}

// addIORatio writes one point per source point, keeping its attributes (direction).
func addIORatio(scope pmetric.ScopeMetrics, src pmetric.Metric, uptime float64) {
	m := scope.Metrics().AppendEmpty()
	m.SetName(ioRatioMetric)
	m.SetDescription("Bytes read or written over the life of the process, per second of age")
	m.SetUnit("By/s")
	out := m.SetEmptyGauge().DataPoints()
	dps := src.Sum().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		dp := out.AppendEmpty()
		dps.At(i).Attributes().CopyTo(dp.Attributes())
		dp.SetDoubleValue(pointValue(dps.At(i)) / uptime)
		dp.SetTimestamp(dps.At(i).Timestamp())
	}
}
