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

// Builder collects the points of one BMC scrape under one resource. Every
// metric is a gauge, created the first time a point needs it.
type Builder struct {
	md      pmetric.Metrics
	sm      pmetric.ScopeMetrics
	ts      pcommon.Timestamp
	metrics map[string]pmetric.Gauge
}

// NewBuilder starts a scrape of the BMC described by res at time now.
func NewBuilder(scope string, res Resource, now time.Time) *Builder {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	attrs := rm.Resource().Attributes()
	attrs.PutStr(ResHostName, res.HostName)
	attrs.PutStr(AttrVendor, res.Vendor)
	attrs.PutStr(ResAddress, res.ServerAddress)
	attrs.PutStr(ResProtocol, res.Protocol)
	for key, v := range map[string]string{AttrModel: res.Model, AttrSerialNumber: res.Serial, AttrFirmwareVersion: res.Firmware} {
		if v != "" {
			attrs.PutStr(key, v)
		}
	}
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scope)
	return &Builder{md: md, sm: sm, ts: pcommon.NewTimestampFromTime(now), metrics: map[string]pmetric.Gauge{}}
}

// Metrics returns the collected points.
func (b *Builder) Metrics() pmetric.Metrics { return b.md }

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
	dp.Attributes().PutStr(AttrID, id)
	dp.Attributes().PutStr(AttrName, name)
	dp.Attributes().PutStr(AttrParent, parent)
}

// Temperature adds hw.temperature in degrees Celsius.
func (b *Builder) Temperature(id, name, parent string, celsius float64) {
	b.sensor(MetricTemperature, "Cel", id, name, parent, celsius)
}

// TemperatureLimit adds hw.temperature.limit with its hw.limit_type.
func (b *Builder) TemperatureLimit(id, name, parent, limitType string, celsius float64) {
	b.sensor(MetricTemperatureLimit, "Cel", id, name, parent, celsius).Attributes().PutStr(AttrLimitType, limitType)
}

// FanSpeed adds hw.fan.speed in rpm.
func (b *Builder) FanSpeed(id, name, parent string, rpm float64) {
	b.sensor(MetricFanSpeed, "rpm", id, name, parent, rpm)
}

// FanSpeedRatio adds hw.fan.speed_ratio, a fraction between 0 and 1.
func (b *Builder) FanSpeedRatio(id, name, parent string, ratio float64) {
	b.sensor(MetricFanSpeedRatio, "1", id, name, parent, ratio)
}

// Voltage adds hw.voltage in volts.
func (b *Builder) Voltage(id, name, parent string, volts float64) {
	b.sensor(MetricVoltage, "V", id, name, parent, volts)
}

// HostPower adds hw.host.power in watts; it has no identity attributes.
func (b *Builder) HostPower(watts float64) {
	b.point(MetricHostPower, "W").SetDoubleValue(watts)
}

// PowerSupplyUsage adds hw.power_supply.usage in watts.
func (b *Builder) PowerSupplyUsage(id, name, parent string, watts float64) {
	b.sensor(MetricPowerSupplyUsage, "W", id, name, parent, watts)
}

// PowerSupplyLimit adds hw.power_supply.limit in watts.
func (b *Builder) PowerSupplyLimit(id, name, parent string, watts float64) {
	b.sensor(MetricPowerSupplyLimit, "W", id, name, parent, watts)
}

// PowerSupplyUtilization adds hw.power_supply.utilization, a fraction.
func (b *Builder) PowerSupplyUtilization(id, name, parent string, ratio float64) {
	b.sensor(MetricPowerSupplyUtilization, "1", id, name, parent, ratio)
}

// PhysicalDiskSize adds hw.physical_disk.size in bytes.
func (b *Builder) PhysicalDiskSize(id, name, parent string, bytes float64) {
	b.sensor(MetricPhysicalDiskSize, "By", id, name, parent, bytes)
}

// PhysicalDiskEndurance adds hw.physical_disk.endurance_utilization, the
// fraction of the drive's life used, with hw.physical_disk.state "used".
func (b *Builder) PhysicalDiskEndurance(id, name, parent string, ratio float64) {
	b.sensor(MetricPhysicalDiskEndurance, "1", id, name, parent, ratio).Attributes().PutStr(AttrPhysicalDiskState, DiskStateUsed)
}

// MemorySize adds hw.memory.size in bytes.
func (b *Builder) MemorySize(id, name, parent string, bytes float64) {
	b.sensor(MetricMemorySize, "By", id, name, parent, bytes)
}

// Status adds hw.status for one component: one point per state, 1 for the
// current state and 0 for the others.
func (b *Builder) Status(hwType, id, name, current string) {
	for _, s := range States {
		dp := b.point(MetricStatus, "1")
		if s == current {
			dp.SetIntValue(1)
		} else {
			dp.SetIntValue(0)
		}
		dp.Attributes().PutStr(AttrID, id)
		dp.Attributes().PutStr(AttrName, name)
		dp.Attributes().PutStr(AttrType, hwType)
		dp.Attributes().PutStr(AttrState, s)
	}
}

// BMCUp adds hw.bmc.up. A down BMC carries the reason; an up one does not.
func (b *Builder) BMCUp(up bool, reason string) {
	dp := b.point(MetricBMCUp, "1")
	if up {
		dp.SetIntValue(1)
		return
	}
	dp.SetIntValue(0)
	dp.Attributes().PutStr(AttrReason, reason)
}

// BMCScrapeDuration adds hw.bmc.scrape.duration in seconds.
func (b *Builder) BMCScrapeDuration(d time.Duration) {
	b.point(MetricBMCScrapeDuration, "s").SetDoubleValue(d.Seconds())
}
