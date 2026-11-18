// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"fmt"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// builder collects the data points of one scrape under one resource and one scope.
type builder struct {
	sm      pmetric.ScopeMetrics
	metrics map[string]pmetric.Metric
	start   pcommon.Timestamp
	ts      pcommon.Timestamp
}

func newBuilder(md pmetric.Metrics, start, ts pcommon.Timestamp) *builder {
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	return &builder{sm: sm, metrics: map[string]pmetric.Metric{}, start: start, ts: ts}
}

// metric returns the metric of a name, made from its hwschema spec on first use.
func (b *builder) metric(name string) (pmetric.Metric, hwschema.MetricSpec) {
	spec, ok := hwschema.LookupMetric(name)
	if !ok {
		panic(fmt.Sprintf("smartreceiver: metric %q is not in hwschema", name))
	}
	if m, ok := b.metrics[name]; ok {
		return m, spec
	}
	m := b.sm.Metrics().AppendEmpty()
	m.SetName(spec.Name)
	m.SetUnit(spec.Unit)
	m.SetDescription(spec.Description)
	if spec.Kind == hwschema.Sum {
		sum := m.SetEmptySum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	} else {
		m.SetEmptyGauge()
	}
	b.metrics[name] = m
	return m, spec
}

// point appends a data point of a metric with the identity of the disk and the extra attribute pairs.
func (b *builder) point(name string, id ident, kv ...string) pmetric.NumberDataPoint {
	m, spec := b.metric(name)
	var dp pmetric.NumberDataPoint
	if spec.Kind == hwschema.Sum {
		dp = m.Sum().DataPoints().AppendEmpty()
		dp.SetStartTimestamp(b.start)
	} else {
		dp = m.Gauge().DataPoints().AppendEmpty()
	}
	dp.SetTimestamp(b.ts)
	id.put(dp.Attributes())
	for i := 0; i+1 < len(kv); i += 2 {
		dp.Attributes().PutStr(kv[i], kv[i+1])
	}
	return dp
}

func (b *builder) gauge(name string, id ident, value float64, kv ...string) {
	b.point(name, id, kv...).SetDoubleValue(value)
}

func (b *builder) count(name string, id ident, value uint64, kv ...string) {
	b.point(name, id, kv...).SetIntValue(clampInt64(value))
}

func clampInt64(v uint64) int64 {
	if v > 1<<63-1 {
		return 1<<63 - 1
	}
	return int64(v)
}

// ident is what every series of one disk carries.
type ident struct {
	id, name, model, serial, firmware, diskType, vendor string
}

func (i ident) put(attrs pcommon.Map) {
	attrs.PutStr(hwschema.AttrID, i.id)
	attrs.PutStr(hwschema.AttrName, i.name)
	for key, value := range map[string]string{
		hwschema.AttrModel:            i.model,
		hwschema.AttrSerialNumber:     i.serial,
		hwschema.AttrFirmwareVersion:  i.firmware,
		hwschema.AttrPhysicalDiskType: i.diskType,
		hwschema.AttrVendor:           i.vendor,
	} {
		if value != "" {
			attrs.PutStr(key, value)
		}
	}
}

// firstNonZero returns the first argument that is not 0.
func firstNonZero(values ...uint64) uint64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// diskStates are the hw.state values a disk status metric emits.
var diskStates = []string{hwschema.StateOK, hwschema.StateDegraded, hwschema.StatePredictedFailure, hwschema.StateFailed}

// status emits one hw.status point for each state: 1 for the current one and 0 for the others.
func (b *builder) status(id ident, current string) {
	for _, state := range diskStates {
		v := 0.0
		if state == current {
			v = 1
		}
		b.gauge(hwschema.MetricStatus, id, v, hwschema.AttrType, hwschema.TypePhysicalDisk, hwschema.AttrState, state)
	}
}
