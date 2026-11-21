// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
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

// metric makes the metric from its hwschema spec on first use.
func (b *builder) metric(name string) (pmetric.Metric, hwschema.MetricSpec) {
	spec, ok := hwschema.LookupMetric(name)
	if !ok {
		panic(fmt.Sprintf("windiskhealthreceiver: metric %q is not in hwschema", name))
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

// point adds a data point; attrs are key, value pairs and an odd count is a programming error.
func (b *builder) point(name string, id ident, attrs ...string) pmetric.NumberDataPoint {
	if len(attrs)%2 != 0 {
		panic(fmt.Sprintf("windiskhealthreceiver: metric %q has an odd attribute list", name))
	}
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
	for i := 0; i < len(attrs); i += 2 {
		dp.Attributes().PutStr(attrs[i], attrs[i+1])
	}
	return dp
}

func (b *builder) gauge(name string, id ident, value float64, attrs ...string) {
	b.point(name, id, attrs...).SetDoubleValue(value)
}

func (b *builder) count(name string, id ident, value uint64, attrs ...string) {
	b.point(name, id, attrs...).SetIntValue(clampInt64(value))
}

// multiplySaturating multiplies and stops at the largest value instead of wrapping.
func multiplySaturating(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return math.MaxUint64
	}
	return lo
}

func clampInt64(v uint64) int64 {
	if v > 1<<63-1 {
		return 1<<63 - 1
	}
	return int64(v)
}

// ident is what every series of one disk carries.
type ident struct {
	id, name, model, serial, firmware, diskType string
}

func (i ident) put(attrs pcommon.Map) {
	attrs.PutStr(hwschema.AttrID, hwschema.ValidUTF8(i.id))
	attrs.PutStr(hwschema.AttrName, hwschema.ValidUTF8(i.name))
	for key, value := range map[string]string{
		hwschema.AttrModel:            i.model,
		hwschema.AttrSerialNumber:     i.serial,
		hwschema.AttrFirmwareVersion:  i.firmware,
		hwschema.AttrPhysicalDiskType: i.diskType,
	} {
		if value != "" {
			attrs.PutStr(key, hwschema.ValidUTF8(value))
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// diskStates are the hw.state values a disk status metric emits, best first.
var diskStates = []string{hwschema.StateOK, hwschema.StateDegraded, hwschema.StatePredictedFailure, hwschema.StateFailed}

// worse picks by the order of diskStates: later is worse.
func worse(a, b string) string {
	rank := func(s string) int {
		for i, v := range diskStates {
			if v == s {
				return i
			}
		}
		return -1
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

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

// temperature emits hw.temperature; an NVMe disk labels it as the composite sensor, as the smart receiver does.
func (b *builder) temperature(id ident, celsius float64) {
	if id.diskType == hwschema.DiskNVMe {
		b.gauge(hwschema.MetricTemperature, id, celsius, hwschema.AttrSensorLocation, "composite")
		return
	}
	b.gauge(hwschema.MetricTemperature, id, celsius)
}

// SMART attribute ids the receiver reads by number.
const (
	smartReallocatedSectors  uint8 = 5
	smartPowerOnHours        uint8 = 9
	smartPowerCycleCount     uint8 = 12
	smartAirflowTemperature  uint8 = 190
	smartTemperature         uint8 = 194
	smartPendingSectors      uint8 = 197
	smartUncorrectableErrors uint8 = 198
	smartUdmaCrcErrors       uint8 = 199
	smartSSDLifeLeft         uint8 = 231
	smartMediaWearout        uint8 = 233
	smartAvailableReserved   uint8 = 202
	smartTotalWritten        uint8 = 241
	smartTotalRead           uint8 = 242
)

// The attribute table the receiver emits is fixed: the default ids, normalized and raw values.
var attributeValueTypes = []string{hwschema.ValueNormalized, hwschema.ValueRaw}

func findAttr(attrs []smartdecode.Attribute, id uint8) (smartdecode.Attribute, bool) {
	for _, a := range attrs {
		if a.ID == id {
			return a, true
		}
	}
	return smartdecode.Attribute{}, false
}

// emitNVMe emits the series of one NVMe SMART/Health log; the composite temperature is left to the caller.
func (b *builder) emitNVMe(id ident, data smartdecode.NVMeData, withTemperature bool) {
	smart := data.Smart
	if data.Identify.SizeBytes > 0 {
		b.count(hwschema.MetricPhysicalDiskSize, id, data.Identify.SizeBytes)
	}
	if withTemperature && smart.HasComposite {
		b.temperature(id, float64(smart.CompositeCelsius))
	}
	for _, sensor := range smart.SensorsCelsius {
		b.gauge(hwschema.MetricTemperature, id, float64(sensor.Celsius), hwschema.AttrSensorLocation, "sensor"+strconv.Itoa(sensor.Index))
	}
	// The log page allows a value above 100 for a drive past its rated life; the ratio stops at 0.
	used := min(smart.PercentageUsed, 100)
	b.gauge(hwschema.MetricPhysicalDiskEndurance, id, 1-float64(used)/100, hwschema.AttrPhysicalDiskState, hwschema.DiskStateRemaining)
	b.count(hwschema.MetricErrors, id, smart.MediaErrors, hwschema.AttrErrorType, hwschema.ErrorMedia)
	b.gauge(hwschema.MetricPhysicalDiskPowerOnTime, id, float64(smart.PowerOnHours)*3600)
	b.count(hwschema.MetricPhysicalDiskPowerCycles, id, smart.PowerCycles)
	b.count(hwschema.MetricPhysicalDiskIO, id, smart.BytesRead(), hwschema.AttrDiskIODir, hwschema.DirectionRead)
	b.count(hwschema.MetricPhysicalDiskIO, id, smart.BytesWritten(), hwschema.AttrDiskIODir, hwschema.DirectionWrite)
	for name, v := range map[string]uint64{
		"Available_Spare":           uint64(smart.AvailableSpare),
		"Available_Spare_Threshold": uint64(smart.SpareThreshold),
		"Unsafe_Shutdowns":          smart.UnsafeShutdowns,
	} {
		b.count(hwschema.MetricPhysicalDiskSmart, id, v, hwschema.AttrSmartAttribute, name, hwschema.AttrSmartValueType, hwschema.ValueRaw)
	}
}

// emitATA emits the series of one SATA SMART read; the temperature is left to the caller.
func (b *builder) emitATA(id ident, data smartdecode.ATAData, withTemperature bool) {
	info := data.Identify
	if info.SizeBytes > 0 {
		b.count(hwschema.MetricPhysicalDiskSize, id, info.SizeBytes)
	}
	if withTemperature {
		if temp, ok := ataTemperature(data); ok {
			b.temperature(id, temp)
		}
	}
	if info.IsSSD() {
		for _, endurance := range []uint8{smartSSDLifeLeft, smartMediaWearout, smartAvailableReserved} {
			if a, ok := findAttr(data.Attributes, endurance); ok {
				b.gauge(hwschema.MetricPhysicalDiskEndurance, id, float64(a.Value)/100, hwschema.AttrPhysicalDiskState, hwschema.DiskStateRemaining)
				break
			}
		}
	}
	for _, e := range []struct {
		id   uint8
		kind string
	}{{smartReallocatedSectors, hwschema.ErrorBadSector}, {smartPendingSectors, hwschema.ErrorBadSector}, {smartUncorrectableErrors, hwschema.ErrorBadSector}, {smartUdmaCrcErrors, hwschema.ErrorCRC}} {
		if a, ok := findAttr(data.Attributes, e.id); ok {
			b.count(hwschema.MetricErrors, id, a.Raw, hwschema.AttrErrorType, e.kind, hwschema.AttrSmartAttribute, smartdecode.AttributeName(e.id))
		}
	}
	if a, ok := findAttr(data.Attributes, smartPowerOnHours); ok {
		b.gauge(hwschema.MetricPhysicalDiskPowerOnTime, id, float64(a.Raw&0xFFFFFFFF)*3600)
	}
	if a, ok := findAttr(data.Attributes, smartPowerCycleCount); ok {
		b.count(hwschema.MetricPhysicalDiskPowerCycles, id, a.Raw)
	}
	for _, m := range []struct {
		id  uint8
		dir string
	}{{smartTotalWritten, hwschema.DirectionWrite}, {smartTotalRead, hwschema.DirectionRead}} {
		a, ok := findAttr(data.Attributes, m.id)
		if !ok {
			continue
		}
		if unit, named := smartdecode.IOUnitBytes(m.id); named {
			b.count(hwschema.MetricPhysicalDiskIO, id, multiplySaturating(a.Raw, unit), hwschema.AttrDiskIODir, m.dir)
		}
	}
	wanted := map[uint8]bool{}
	for _, n := range smartdecode.DefaultAttributeIDs() {
		wanted[n] = true
	}
	for _, a := range data.Attributes {
		if wanted[a.ID] {
			b.emitAttribute(id, a)
		}
	}
}

func (b *builder) emitAttribute(id ident, a smartdecode.Attribute) {
	name := smartdecode.AttributeName(a.ID)
	for _, vt := range attributeValueTypes {
		v := a.Raw
		if vt == hwschema.ValueNormalized {
			v = uint64(a.Value)
		}
		b.gauge(hwschema.MetricPhysicalDiskSmart, id, float64(v), hwschema.AttrSmartAttribute, name, hwschema.AttrSmartValueType, vt)
	}
}

// ataTemperature takes the low byte of attribute 194 (smartTemperature), else 190, else the Device Statistics value.
func ataTemperature(data smartdecode.ATAData) (float64, bool) {
	for _, id := range []uint8{smartTemperature, smartAirflowTemperature} {
		if a, ok := findAttr(data.Attributes, id); ok {
			return float64(a.Raw & 0xFF), true
		}
	}
	if data.HasStatsTemperature {
		return float64(data.StatsTemperature), true
	}
	return 0, false
}
