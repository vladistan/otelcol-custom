// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// Resource describes one BMC; it becomes the attributes of the resource.
type Resource struct {
	HostName      string // the target name
	Vendor        string // dell or supermicro
	ServerAddress string // the BMC address
	Protocol      string // ProtocolRedfish or ProtocolIPMI
	Model         string // empty when the BMC does not report it
	Serial        string
	Firmware      string
}

// Builder collects the points of one BMC scrape under one resource. A metric
// is created the first time a point needs it. All are gauges except
// hw.bmc.scrape.errors, a cumulative sum.
type Builder struct {
	md      pmetric.Metrics
	sm      pmetric.ScopeMetrics
	ts      pcommon.Timestamp
	metrics map[string]pmetric.Gauge
	sums    map[string]pmetric.Sum
}

// NewBuilder starts a scrape of the BMC described by res at time now.
func NewBuilder(scope string, res Resource, now time.Time) *Builder {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	attrs := rm.Resource().Attributes()
	attrs.PutStr(ResHostName, ValidUTF8(res.HostName))
	attrs.PutStr(AttrVendor, ValidUTF8(res.Vendor))
	attrs.PutStr(ResAddress, ValidUTF8(res.ServerAddress))
	attrs.PutStr(ResProtocol, ValidUTF8(res.Protocol))
	for key, v := range map[string]string{AttrModel: res.Model, AttrSerialNumber: res.Serial, AttrFirmwareVersion: res.Firmware} {
		if v != "" {
			attrs.PutStr(key, ValidUTF8(v))
		}
	}
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scope)
	return &Builder{md: md, sm: sm, ts: pcommon.NewTimestampFromTime(now), metrics: map[string]pmetric.Gauge{}, sums: map[string]pmetric.Sum{}}
}

// Component starts a resource for one part of the BMC, such as a drive. It holds the attributes of the BMC
// resource with the overrides applied; an empty override value is skipped. The returned builder writes to the
// same metrics value.
func (b *Builder) Component(overrides map[string]string) *Builder {
	rm := b.md.ResourceMetrics().AppendEmpty()
	b.md.ResourceMetrics().At(0).Resource().Attributes().CopyTo(rm.Resource().Attributes())
	for k, v := range overrides {
		if v != "" {
			rm.Resource().Attributes().PutStr(k, ValidUTF8(v))
		}
	}
	sm := rm.ScopeMetrics().AppendEmpty()
	b.sm.Scope().CopyTo(sm.Scope())
	return &Builder{md: b.md, sm: sm, ts: b.ts, metrics: map[string]pmetric.Gauge{}, sums: map[string]pmetric.Sum{}}
}

// Metrics returns the one value every builder from Component writes to, so a
// caller can read it from any of them.
func (b *Builder) Metrics() pmetric.Metrics { return b.md }

// boolInt is the 0/1 value of a flag metric.
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func (b *Builder) point(name, unit string) pmetric.NumberDataPoint {
	g, ok := b.metrics[name]
	if !ok {
		m := b.sm.Metrics().AppendEmpty()
		m.SetName(name)
		m.SetUnit(unit)
		g = m.SetEmptyGauge()
		b.metrics[name] = g
	}
	dp := g.DataPoints().AppendEmpty()
	dp.SetTimestamp(b.ts)
	return dp
}

// sensor adds a double point with the three identity attributes.
func (b *Builder) sensor(name, unit, id, hwName, parent string, v float64) pmetric.NumberDataPoint {
	dp := b.point(name, unit)
	dp.SetDoubleValue(v)
	identity(dp, id, hwName, parent)
	return dp
}

func identity(dp pmetric.NumberDataPoint, id, name, parent string) {
	dp.Attributes().PutStr(AttrID, ValidUTF8(id))
	dp.Attributes().PutStr(AttrName, ValidUTF8(name))
	dp.Attributes().PutStr(AttrParent, ValidUTF8(parent))
}

// Temperature adds hw.temperature in degrees Celsius.
func (b *Builder) Temperature(id, name, parent string, celsius float64) {
	b.sensor(MetricTemperature, UnitCelsius, id, name, parent, celsius)
}

// TemperatureLimit adds hw.temperature.limit with its hw.limit_type.
func (b *Builder) TemperatureLimit(id, name, parent, limitType string, celsius float64) {
	b.sensor(MetricTemperatureLimit, UnitCelsius, id, name, parent, celsius).Attributes().PutStr(AttrLimitType, ValidUTF8(limitType))
}

// FanSpeed adds hw.fan.speed in rpm.
func (b *Builder) FanSpeed(id, name, parent string, rpm float64) {
	b.sensor(MetricFanSpeed, UnitRPM, id, name, parent, rpm)
}

// FanSpeedRatio adds hw.fan.speed_ratio, a fraction between 0 and 1.
func (b *Builder) FanSpeedRatio(id, name, parent string, ratio float64) {
	b.sensor(MetricFanSpeedRatio, UnitRatio, id, name, parent, ratio)
}

// Voltage adds hw.voltage in volts.
func (b *Builder) Voltage(id, name, parent string, volts float64) {
	b.sensor(MetricVoltage, UnitVolts, id, name, parent, volts)
}

// HostPower adds hw.host.power in watts; it has no identity attributes.
func (b *Builder) HostPower(watts float64) {
	b.point(MetricHostPower, UnitWatts).SetDoubleValue(watts)
}

// PowerSupplyUsage adds hw.power_supply.usage in watts.
func (b *Builder) PowerSupplyUsage(id, name, parent string, watts float64) {
	b.sensor(MetricPowerSupplyUsage, UnitWatts, id, name, parent, watts)
}

// PowerSupplyLimit adds hw.power_supply.limit in watts.
func (b *Builder) PowerSupplyLimit(id, name, parent string, watts float64) {
	b.sensor(MetricPowerSupplyLimit, UnitWatts, id, name, parent, watts)
}

// PowerSupplyUtilization adds hw.power_supply.utilization, a fraction.
func (b *Builder) PowerSupplyUtilization(id, name, parent string, ratio float64) {
	b.sensor(MetricPowerSupplyUtilization, UnitRatio, id, name, parent, ratio)
}

// PhysicalDiskSize adds hw.physical_disk.size in bytes.
func (b *Builder) PhysicalDiskSize(id, name, parent string, bytes float64) {
	b.sensor(MetricPhysicalDiskSize, UnitBytes, id, name, parent, bytes)
}

// PhysicalDiskEndurance adds hw.physical_disk.endurance_utilization, the
// fraction of the drive's life used, with hw.physical_disk.state "used".
func (b *Builder) PhysicalDiskEndurance(id, name, parent string, ratio float64) {
	b.sensor(MetricPhysicalDiskEndurance, UnitRatio, id, name, parent, ratio).Attributes().PutStr(AttrPhysicalDiskState, DiskStateUsed)
}

// MemorySize adds hw.memory.size in bytes.
func (b *Builder) MemorySize(id, name, parent string, bytes float64) {
	b.sensor(MetricMemorySize, UnitBytes, id, name, parent, bytes)
}

// Status adds hw.status for one component: one point per state, 1 for the
// current state and 0 for the others.
func (b *Builder) Status(hwType, id, name, current string) {
	b.StatusWith(hwType, id, name, current, nil)
}

// StatusWith is Status with more attributes on every point; an empty value is left out.
func (b *Builder) StatusWith(hwType, id, name, current string, extra map[string]string) {
	for _, s := range States {
		dp := b.point(MetricStatus, UnitRatio)
		dp.SetIntValue(boolInt(s == current))
		dp.Attributes().PutStr(AttrID, ValidUTF8(id))
		dp.Attributes().PutStr(AttrName, ValidUTF8(name))
		dp.Attributes().PutStr(AttrType, ValidUTF8(hwType))
		dp.Attributes().PutStr(AttrState, s)
		for k, v := range extra {
			if v != "" {
				dp.Attributes().PutStr(k, ValidUTF8(v))
			}
		}
	}
}

// DiskControllerSpeed adds hw.disk_controller.speed in Mbit/s.
func (b *Builder) DiskControllerSpeed(id, name, parent string, megabits float64) {
	b.sensor(MetricDiskControllerSpeed, UnitMegabitPerSecond, id, name, parent, megabits)
}

// LogicalDiskLimit adds hw.logical_disk.limit, the capacity of a volume in bytes, with extra attributes.
func (b *Builder) LogicalDiskLimit(id, name, parent string, bytes float64, extra map[string]string) {
	dp := b.sensor(MetricLogicalDiskLimit, UnitBytes, id, name, parent, bytes)
	for k, v := range extra {
		if v != "" {
			dp.Attributes().PutStr(k, ValidUTF8(v))
		}
	}
}

// PhysicalDiskIndicator adds hw.physical_disk.indicator: 1 when the locate LED of the drive is on, else 0.
func (b *Builder) PhysicalDiskIndicator(id, name, parent string, active bool) {
	dp := b.point(MetricPhysicalDiskIndicator, UnitRatio)
	dp.SetIntValue(boolInt(active))
	identity(dp, id, name, parent)
}

// PhysicalDiskPredictedFailure adds hw.physical_disk.predicted_failure: 1 when the BMC predicts the drive will
// fail, else 0. It is apart from hw.status, so a predicted failure never hides the health.
func (b *Builder) PhysicalDiskPredictedFailure(id, name, parent string, predicted bool) {
	dp := b.point(MetricPhysicalDiskPredictedFailure, UnitRatio)
	dp.SetIntValue(boolInt(predicted))
	identity(dp, id, name, parent)
}

// LogicalDiskMediaSpanCount adds hw.logical_disk.media_span_count, the spans of a volume.
func (b *Builder) LogicalDiskMediaSpanCount(id, name, parent string, spans float64) {
	b.sensor(MetricLogicalDiskMediaSpanCount, UnitSpans, id, name, parent, spans)
}

// StorageDriveCount adds hw.storage.drive_count, the drives a storage resource lists.
func (b *Builder) StorageDriveCount(id, name string, drives float64) {
	b.sensor(MetricStorageDriveCount, UnitDrives, id, name, "", drives)
}

// ScrapeErrors adds hw.bmc.scrape.errors, the running count of failed reads of one path group. It is a
// cumulative monotonic sum.
func (b *Builder) ScrapeErrors(group string, total int64) {
	sum, ok := b.sums[MetricBMCScrapeErrors]
	if !ok {
		m := b.sm.Metrics().AppendEmpty()
		m.SetName(MetricBMCScrapeErrors)
		m.SetUnit(UnitErrors)
		sum = m.SetEmptySum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		b.sums[MetricBMCScrapeErrors] = sum
	}
	dp := sum.DataPoints().AppendEmpty()
	dp.SetTimestamp(b.ts)
	dp.SetIntValue(total)
	dp.Attributes().PutStr(AttrPathGroup, ValidUTF8(group))
}

// BMCUp adds hw.bmc.up. A down BMC carries the reason; an up one does not.
func (b *Builder) BMCUp(up bool, reason string) {
	dp := b.point(MetricBMCUp, UnitRatio)
	if up {
		dp.SetIntValue(1)
		return
	}
	dp.SetIntValue(0)
	dp.Attributes().PutStr(AttrReason, ValidUTF8(reason))
}

// FRUInfo adds inventory.bmc.fru.info, an info gauge of value 1 with the
// fru.type and the non-empty fields of one FRU record.
func (b *Builder) FRUInfo(fruType string, fields map[string]string) {
	dp := b.point(MetricBMCFRUInfo, UnitRatio)
	dp.SetIntValue(1)
	dp.Attributes().PutStr(AttrFRUType, ValidUTF8(fruType))
	for k, v := range fields {
		if v != "" {
			dp.Attributes().PutStr(k, ValidUTF8(v))
		}
	}
}

// InventoryStatus adds inventory.collection.status for one source: 1 when it was read, 0 when it was not.
func (b *Builder) InventoryStatus(source string, read bool) {
	dp := b.point(MetricInventoryStatus, UnitRatio)
	dp.SetIntValue(boolInt(read))
	dp.Attributes().PutStr(AttrInventorySource, ValidUTF8(source))
}

// BMCScrapeDuration adds hw.bmc.scrape.duration in seconds.
func (b *Builder) BMCScrapeDuration(d time.Duration) {
	b.point(MetricBMCScrapeDuration, UnitSeconds).SetDoubleValue(d.Seconds())
}
