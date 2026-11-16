// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func testResource() Resource {
	return Resource{
		HostName: "pve12", Vendor: "supermicro", ServerAddress: "192.0.2.12", Protocol: "ipmi",
		Model: "X9DRW", Serial: "S123", Firmware: "3.53",
	}
}

func find(t *testing.T, md pmetric.Metrics, name string) pmetric.Metric {
	t.Helper()
	require.Equal(t, 1, md.ResourceMetrics().Len())
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i)
		}
	}
	t.Fatalf("metric %s not found", name)
	return pmetric.Metric{}
}

func attrMap(a pcommon.Map) map[string]string {
	m := map[string]string{}
	a.Range(func(k string, v pcommon.Value) bool {
		m[k] = v.AsString()
		return true
	})
	return m
}

func TestResourceCarriesTheSevenAttributes(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.Temperature("t1", "CPU Temp", "cpu", 40)

	got := attrMap(b.Metrics().ResourceMetrics().At(0).Resource().Attributes())

	assert.Equal(t, map[string]string{
		"host.name": "pve12", "hw.vendor": "supermicro", "server.address": "192.0.2.12", "hw.bmc.protocol": "ipmi",
		"hw.model": "X9DRW", "hw.serial_number": "S123", "hw.firmware_version": "3.53",
	}, got)
}

func TestResourceOmitsWhatTheBMCDoesNotReport(t *testing.T) {
	res := testResource()
	res.Model, res.Serial, res.Firmware = "", "", ""
	b := NewBuilder("scope", res, time.Unix(100, 0))
	b.BMCUp(true, "")

	got := attrMap(b.Metrics().ResourceMetrics().At(0).Resource().Attributes())

	assert.Len(t, got, 4)
	assert.NotContains(t, got, "hw.model")
}

func TestTemperatureAndLimit(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.Temperature("coretemp/1", "CPU1 Temp", "cpu", 40.5)
	b.TemperatureLimit("coretemp/1", "CPU1 Temp", "cpu", LimitCritical, 95)

	md := b.Metrics()

	temp := find(t, md, MetricTemperature)
	assert.Equal(t, "Cel", temp.Unit())
	dp := temp.Gauge().DataPoints().At(0)
	assert.InDelta(t, 40.5, dp.DoubleValue(), 1e-9)
	assert.Equal(t, map[string]string{"hw.id": "coretemp/1", "hw.name": "CPU1 Temp", "hw.parent": "cpu"}, attrMap(dp.Attributes()))
	lim := find(t, md, MetricTemperatureLimit)
	assert.Equal(t, "Cel", lim.Unit())
	assert.Equal(t, "critical", attrMap(lim.Gauge().DataPoints().At(0).Attributes())["hw.limit_type"])
}

func TestFanVoltagePowerAndMemoryUnits(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.FanSpeed("f1", "FAN1", "", 4950)
	b.FanSpeedRatio("f2", "FAN2", "", 0.4)
	b.Voltage("v1", "12V", "", 12.1)
	b.HostPower(250)
	b.PowerSupplyUsage("p1", "PSU1", "", 120)
	b.PowerSupplyLimit("p1", "PSU1", "", 750)
	b.PowerSupplyUtilization("p1", "PSU1", "", 0.16)
	b.PhysicalDiskSize("d1", "Disk 0", "", 480e9)
	b.PhysicalDiskEndurance("d1", "Disk 0", "", 0.2)
	b.MemorySize("m1", "DIMM A1", "", 16e9)
	md := b.Metrics()

	for name, unit := range map[string]string{
		MetricFanSpeed: "rpm", MetricFanSpeedRatio: "1", MetricVoltage: "V", MetricHostPower: "W",
		MetricPowerSupplyUsage: "W", MetricPowerSupplyLimit: "W", MetricPowerSupplyUtilization: "1",
		MetricPhysicalDiskSize: "By", MetricPhysicalDiskEndurance: "1", MetricMemorySize: "By",
	} {
		assert.Equal(t, unit, find(t, md, name).Unit(), name)
	}
	disk := find(t, md, MetricPhysicalDiskEndurance).Gauge().DataPoints().At(0)
	assert.Equal(t, "used", attrMap(disk.Attributes())["hw.physical_disk.state"])
	assert.InDelta(t, 0.2, disk.DoubleValue(), 1e-9)
	assert.Equal(t, int64(250), int64(find(t, md, MetricHostPower).Gauge().DataPoints().At(0).DoubleValue()))
}

func TestStatusHasOnePointPerStateAndOnlyTheCurrentOneIs1(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.Status(TypeFan, "f1", "FAN1", StateDegraded)

	dps := find(t, b.Metrics(), MetricStatus).Gauge().DataPoints()

	require.Equal(t, 5, dps.Len())
	got := map[string]int64{}
	for i := 0; i < dps.Len(); i++ {
		a := attrMap(dps.At(i).Attributes())
		assert.Equal(t, "fan", a["hw.type"])
		got[a["hw.state"]] = dps.At(i).IntValue()
	}
	assert.Equal(t, map[string]int64{"ok": 0, "degraded": 1, "failed": 0, "needs_cleaning": 0, "predicted_failure": 0}, got)
}

func TestBMCUpCarriesAReasonOnlyWhenDown(t *testing.T) {
	up := NewBuilder("scope", testResource(), time.Unix(100, 0))
	up.BMCUp(true, "auth")
	down := NewBuilder("scope", testResource(), time.Unix(100, 0))
	down.BMCUp(false, ReasonAuth)

	upDP := find(t, up.Metrics(), MetricBMCUp).Gauge().DataPoints().At(0)
	downDP := find(t, down.Metrics(), MetricBMCUp).Gauge().DataPoints().At(0)

	assert.Equal(t, int64(1), upDP.IntValue())
	assert.NotContains(t, attrMap(upDP.Attributes()), "reason")
	assert.Equal(t, int64(0), downDP.IntValue())
	assert.Equal(t, "auth", attrMap(downDP.Attributes())["reason"])
}

func TestBMCScrapeDurationIsInSeconds(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.BMCScrapeDuration(1500 * time.Millisecond)

	m := find(t, b.Metrics(), MetricBMCScrapeDuration)

	assert.Equal(t, "s", m.Unit())
	assert.InDelta(t, 1.5, m.Gauge().DataPoints().At(0).DoubleValue(), 1e-9)
}

func TestEachMetricIsCreatedOnceWithAllItsPoints(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.Temperature("a", "A", "", 1)
	b.Temperature("b", "B", "", 2)

	md := b.Metrics()

	assert.Equal(t, 1, md.MetricCount())
	assert.Equal(t, 2, find(t, md, MetricTemperature).Gauge().DataPoints().Len())
}

func TestPointsCarryTheScrapeTimestampAndScopeName(t *testing.T) {
	now := time.Unix(1700000000, 0)
	b := NewBuilder("my/scope", testResource(), now)
	b.Voltage("v", "V", "", 1)

	md := b.Metrics()

	assert.Equal(t, "my/scope", md.ResourceMetrics().At(0).ScopeMetrics().At(0).Scope().Name())
	assert.Equal(t, pcommon.NewTimestampFromTime(now), find(t, md, MetricVoltage).Gauge().DataPoints().At(0).Timestamp())
}

func TestStateFromHealth(t *testing.T) {
	tests := map[string]struct {
		state string
		ok    bool
	}{
		"OK": {StateOK, true}, "Warning": {StateDegraded, true}, "Critical": {StateFailed, true},
		"ok": {StateOK, true}, "": {"", false}, "Unknown": {"", false},
	}
	for in, want := range tests {
		got, ok := StateFromHealth(in)
		assert.Equal(t, want.ok, ok, in)
		assert.Equal(t, want.state, got, in)
	}
}

func TestMetricNamesListsEveryMetric(t *testing.T) {
	names := BMCMetricNames()

	assert.Contains(t, names, "hw.temperature")
	assert.Contains(t, names, "hw.bmc.up")
	assert.Contains(t, names, "hw.bmc.scrape.duration")
	assert.Len(t, names, 15)
}
