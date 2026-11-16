// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"fmt"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/megaraidreceiver"

// Metric names. hw.* follow the semantic conventions; raid.* name what they lack.
const (
	metricStatus          = hwschema.MetricStatus
	metricTemperature     = hwschema.MetricTemperature
	metricErrors          = hwschema.MetricErrors
	metricPDSize          = hwschema.MetricPhysicalDiskSize
	metricLDLimit         = "hw.logical_disk.limit"
	metricBatteryCharge   = "hw.battery.charge"
	metricBatteryTimeLeft = "hw.battery.time_left"
	metricCtrlInfo        = "raid.controller.info"
	metricReachable       = "raid.controller.reachable"
	metricLDLevel         = "raid.logical_disk.level"
	metricLDStrip         = "raid.logical_disk.strip_size"
	metricLDCache         = "raid.logical_disk.cache_policy"
	metricProgress        = "raid.progress"
	metricBatteryCycles   = "raid.battery.cycles"
	metricBatteryLearn    = "raid.battery.next_learn_time"
	metricSmartState      = "raid.physical_disk.smart_state"
	metricEventErrors     = "otelcol_megaraid_event_read_errors"
)

// Attribute names and values.
const (
	attrOperation  = "raid.operation"
	attrEnclosure  = "raid.enclosure"
	attrSlot       = "raid.slot"
	attrLevel      = "raid.level"
	attrCache      = "raid.cache_policy"
	attrMemory     = "raid.memory.size"
	attrSmartState = "raid.smart_state"
	attrAttrID     = hwschema.AttrSmartAttribute
	attrValueType  = hwschema.AttrSmartValueType
	attrLocation   = hwschema.AttrSensorLocation

	typeController   = "disk_controller"
	typeLogicalDisk  = "logical_disk"
	typePhysicalDisk = hwschema.TypePhysicalDisk
	typeBattery      = "battery"

	errorOther      = "other"
	errorPredictive = "predictive_failure"
)

// states are the hw.state values a status metric emits; needs_cleaning does not apply to drives.
var states = []string{hwschema.StateOK, hwschema.StateDegraded, hwschema.StatePredictedFailure, hwschema.StateFailed}

type kind int

const (
	gauge kind = iota
	sum
)

type spec struct {
	unit string
	kind kind
	desc string
}

var specs = map[string]spec{
	metricStatus:                           {hwschema.UnitRatio, gauge, "Health state of the component; one point for each state, 1 for the current one."},
	metricTemperature:                      {hwschema.UnitCelsius, gauge, "Temperature of a sensor, by hw.sensor_location."},
	metricErrors:                           {hwschema.UnitErrors, sum, "Errors a drive reports, by error.type."},
	metricPDSize:                           {hwschema.UnitBytes, gauge, "Capacity of the physical drive."},
	metricLDLimit:                          {hwschema.UnitBytes, gauge, "Capacity of the logical drive."},
	metricBatteryCharge:                    {hwschema.UnitRatio, gauge, "Charge of the battery as a fraction of full."},
	metricBatteryTimeLeft:                  {hwschema.UnitSeconds, gauge, "Time the battery can run the cache, as the controller reports it."},
	metricCtrlInfo:                         {hwschema.UnitRatio, gauge, "Always 1; the attributes carry product, firmware package, serial number and memory size."},
	metricReachable:                        {hwschema.UnitRatio, gauge, "1 when the controller answers, 0 when the driver reports it unusable."},
	metricLDLevel:                          {hwschema.UnitRatio, gauge, "Always 1; raid.level carries the RAID level."},
	metricLDStrip:                          {hwschema.UnitBytes, gauge, "Strip size of the logical drive."},
	metricLDCache:                          {hwschema.UnitRatio, gauge, "Current cache policy byte of the logical drive; raid.cache_policy has it in hex."},
	metricProgress:                         {hwschema.UnitRatio, gauge, "Progress of a running operation from 0 to 1, by raid.operation."},
	metricBatteryCycles:                    {hwschema.UnitCycles, gauge, "Charge cycles of the battery."},
	metricBatteryLearn:                     {hwschema.UnitSeconds, gauge, "Next battery learn cycle as Unix time."},
	metricEventErrors:                      {hwschema.UnitErrors, sum, "Event log reads that failed, by controller."},
	metricSmartState:                       {hwschema.UnitRatio, gauge, "Always 1; raid.smart_state tells whether SMART was read, is off or is unsupported."},
	hwschema.MetricPhysicalDiskSmart:       {hwschema.UnitRatio, gauge, "One value of one SMART attribute, by attribute and value type."},
	hwschema.MetricPhysicalDiskPowerOnTime: {hwschema.UnitSeconds, gauge, "Time the disk has been powered on."},
	hwschema.MetricPhysicalDiskPowerCycles: {hwschema.UnitCycles, sum, "Number of power cycles of the disk."},
}

// builder collects the data points of one scrape of one controller.
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

func (b *builder) metric(name string) (pmetric.Metric, spec) {
	s, ok := specs[name]
	if !ok {
		panic(fmt.Sprintf("megaraidreceiver: metric %q has no spec", name))
	}
	if m, ok := b.metrics[name]; ok {
		return m, s
	}
	m := b.sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(s.unit)
	m.SetDescription(s.desc)
	if s.kind == sum {
		su := m.SetEmptySum()
		su.SetIsMonotonic(true)
		su.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	} else {
		m.SetEmptyGauge()
	}
	b.metrics[name] = m
	return m, s
}

// ident is what every series of one component carries.
type ident struct {
	id, name, parent string
	extra            []string
}

func (i ident) put(attrs pcommon.Map, typ string) {
	attrs.PutStr(hwschema.AttrID, i.id)
	if i.name != "" {
		attrs.PutStr(hwschema.AttrName, i.name)
	}
	attrs.PutStr(hwschema.AttrType, typ)
	if i.parent != "" {
		attrs.PutStr(hwschema.AttrParent, i.parent)
	}
	for k := 0; k+1 < len(i.extra); k += 2 {
		attrs.PutStr(i.extra[k], i.extra[k+1])
	}
}

func (b *builder) point(name string, id ident, typ string, kv ...string) pmetric.NumberDataPoint {
	m, s := b.metric(name)
	var dp pmetric.NumberDataPoint
	if s.kind == sum {
		dp = m.Sum().DataPoints().AppendEmpty()
		dp.SetStartTimestamp(b.start)
	} else {
		dp = m.Gauge().DataPoints().AppendEmpty()
	}
	dp.SetTimestamp(b.ts)
	id.put(dp.Attributes(), typ)
	for i := 0; i+1 < len(kv); i += 2 {
		dp.Attributes().PutStr(kv[i], kv[i+1])
	}
	return dp
}

func (b *builder) gauge(name string, id ident, typ string, v float64, kv ...string) {
	b.point(name, id, typ, kv...).SetDoubleValue(v)
}

func (b *builder) count(name string, id ident, typ string, v uint64, kv ...string) {
	dp := b.point(name, id, typ, kv...)
	if v > 1<<63-1 {
		v = 1<<63 - 1
	}
	dp.SetIntValue(int64(v)) //nolint:gosec // clamped above
}

// status emits one hw.status point for each state: 1 for the current one, 0 for the others.
func (b *builder) status(id ident, typ, current string) {
	for _, st := range states {
		v := 0.0
		if st == current {
			v = 1
		}
		b.gauge(metricStatus, id, typ, v, hwschema.AttrState, st)
	}
}

func itoa(v int) string { return strconv.Itoa(v) }
