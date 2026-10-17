// Package collect turns raw port readings into one semconv metrics batch per
// scrape and keeps the per-device state that needs history.
package collect

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

// ScopeName scopes every batch; it equals the module import path.
const ScopeName = "github.com/vladistan/otelcol-custom/receiver/gpureceiver"

type instrument int

const (
	gauge instrument = iota
	counter
	upDownCounter
)

type metricSpec struct {
	description string
	unit        string
	instrument  instrument
}

var specs = map[string]metricSpec{
	semconv.MetricUtilization:       {"GPU utilization by task", semconv.UnitRatio, gauge},
	semconv.MetricMemoryUsage:       {"GPU memory in use", semconv.UnitBytes, upDownCounter},
	semconv.MetricMemoryLimit:       {"GPU memory capacity", semconv.UnitBytes, upDownCounter},
	semconv.MetricMemoryUtilization: {"GPU memory in use as a fraction of capacity", semconv.UnitRatio, gauge},
	semconv.MetricIO:                {"Approximate bytes moved over PCIe", semconv.UnitBytes, counter},
	semconv.MetricErrors:            {"GPU ECC errors since the receiver started", semconv.UnitError, counter},
	semconv.MetricStatus:            {"GPU health state, one point per state", semconv.UnitRatio, upDownCounter},
	semconv.MetricTemperature:       {"GPU temperature", semconv.UnitCelsius, gauge},
	semconv.MetricPower:             {"GPU power draw", semconv.UnitWatt, gauge},
	semconv.MetricClock:             {"GPU clock speed by domain", semconv.UnitHertz, gauge},
	semconv.MetricPowerLimit:        {"GPU enforced power limit", semconv.UnitWatt, gauge},
	semconv.MetricPCIeThroughput:    {"GPU PCIe throughput", semconv.UnitBytesPS, gauge},
}

type attr struct{ key, value string }

type batch struct {
	md      pmetric.Metrics
	sm      pmetric.ScopeMetrics
	metrics map[string]pmetric.Metric
	ts      pcommon.Timestamp
	start   pcommon.Timestamp
}

func newBatch(now, start time.Time) *batch {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(ScopeName)
	return &batch{
		md:      md,
		sm:      sm,
		metrics: map[string]pmetric.Metric{},
		ts:      pcommon.NewTimestampFromTime(now),
		start:   pcommon.NewTimestampFromTime(start),
	}
}

func (b *batch) empty() bool { return b.sm.Metrics().Len() == 0 }

func (b *batch) metric(name string) pmetric.Metric {
	if m, ok := b.metrics[name]; ok {
		return m
	}
	spec, ok := specs[name]
	if !ok {
		panic("collect: metric has no spec: " + name)
	}
	m := b.sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetDescription(spec.description)
	m.SetUnit(spec.unit)
	switch spec.instrument {
	case gauge:
		m.SetEmptyGauge()
	case counter, upDownCounter:
		sum := m.SetEmptySum()
		sum.SetIsMonotonic(spec.instrument == counter)
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	}
	b.metrics[name] = m
	return m
}

func (b *batch) point(name string) pmetric.NumberDataPoint {
	m := b.metric(name)
	var dp pmetric.NumberDataPoint
	if m.Type() == pmetric.MetricTypeGauge {
		dp = m.Gauge().DataPoints().AppendEmpty()
	} else {
		dp = m.Sum().DataPoints().AppendEmpty()
		dp.SetStartTimestamp(b.start)
	}
	dp.SetTimestamp(b.ts)
	return dp
}

// addInt adds a point with the six identity attributes plus extra; temperature uses addTemperature.
func (b *batch) addInt(name string, id port.DeviceIdentity, value int64, extra ...attr) {
	dp := b.point(name)
	dp.SetIntValue(value)
	putIdentity(dp.Attributes(), id)
	putExtra(dp.Attributes(), extra)
}

// addDouble is addInt for float values.
func (b *batch) addDouble(name string, id port.DeviceIdentity, value float64, extra ...attr) {
	dp := b.point(name)
	dp.SetDoubleValue(value)
	putIdentity(dp.Attributes(), id)
	putExtra(dp.Attributes(), extra)
}

// addTemperature adds a point scoped to the temperature sensor of a GPU.
func (b *batch) addTemperature(id port.DeviceIdentity, celsius float64) {
	dp := b.point(semconv.MetricTemperature)
	dp.SetDoubleValue(celsius)
	attrs := dp.Attributes()
	attrs.PutStr(semconv.AttrID, id.UUID+":temperature")
	attrs.PutStr(semconv.AttrParent, id.UUID)
	attrs.PutStr(semconv.AttrName, id.Name+" temperature")
	attrs.PutStr(semconv.AttrSensorLocation, semconv.SensorLocationGPU)
}

func putIdentity(attrs pcommon.Map, id port.DeviceIdentity) {
	attrs.PutStr(semconv.AttrID, id.UUID)
	attrs.PutStr(semconv.AttrName, id.Name)
	attrs.PutStr(semconv.AttrModel, id.Model)
	attrs.PutStr(semconv.AttrType, semconv.TypeGPU)
	attrs.PutStr(semconv.AttrVendor, semconv.VendorNvidia)
	if id.DriverVersion != "" {
		attrs.PutStr(semconv.AttrDriverVersion, id.DriverVersion)
	}
}

func putExtra(attrs pcommon.Map, extra []attr) {
	for _, a := range extra {
		attrs.PutStr(a.key, a.value)
	}
}
