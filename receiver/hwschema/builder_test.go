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
}

func TestHostPowerHasNoIdentityAttributes(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.HostPower(250)

	dp := find(t, b.Metrics(), MetricHostPower).Gauge().DataPoints().At(0)

	assert.InDelta(t, 250, dp.DoubleValue(), 1e-9)
	assert.Empty(t, attrMap(dp.Attributes()))
}

func TestPhysicalDiskEnduranceIsTheFractionUsed(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.PhysicalDiskEndurance("d1", "Disk 0", "", 0.2)

	disk := find(t, b.Metrics(), MetricPhysicalDiskEndurance).Gauge().DataPoints().At(0)

	assert.Equal(t, "used", attrMap(disk.Attributes())["hw.physical_disk.state"])
	assert.InDelta(t, 0.2, disk.DoubleValue(), 1e-9)
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

// A name a builder method emits but BMCMetricNames omits would escape the
// source check, so the list is compared with what every method writes.
func TestMetricNamesListsEveryMetricTheBuilderEmits(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.Temperature("i", "n", "p", 1)
	b.TemperatureLimit("i", "n", "p", LimitMax, 1)
	b.FanSpeed("i", "n", "p", 1)
	b.FanSpeedRatio("i", "n", "p", 1)
	b.Voltage("i", "n", "p", 1)
	b.HostPower(1)
	b.PowerSupplyUsage("i", "n", "p", 1)
	b.PowerSupplyLimit("i", "n", "p", 1)
	b.PowerSupplyUtilization("i", "n", "p", 1)
	b.PhysicalDiskSize("i", "n", "p", 1)
	b.PhysicalDiskEndurance("i", "n", "p", 1)
	b.MemorySize("i", "n", "p", 1)
	b.Status(TypeFan, "i", "n", StateOK)
	b.BMCUp(true, "")
	b.BMCScrapeDuration(time.Second)
	b.ScrapeErrors("g", 1)
	b.DiskControllerSpeed("i", "n", "p", 1)
	b.LogicalDiskLimit("i", "n", "p", 1, nil)
	b.PhysicalDiskIndicator("i", "n", "p", true)
	b.PhysicalDiskPredictedFailure("i", "n", "p", true)
	b.LogicalDiskMediaSpanCount("i", "n", "p", 1)
	b.StorageDriveCount("i", "n", 1)
	b.FRUInfo(FRUBoard, nil)
	b.InventoryStatus(SourceFRU, true)
	var emitted []string
	ms := b.Metrics().ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		emitted = append(emitted, ms.At(i).Name())
	}

	assert.ElementsMatch(t, emitted, BMCMetricNames())
}

func TestComponentResourceCarriesItsOverrides(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.BMCUp(true, "")
	d := b.Component(map[string]string{AttrSerialNumber: "DRIVE-1", AttrModel: "", AttrPhysicalDiskType: "SSD"})
	d.PhysicalDiskSize("d1", "Drive 1", "ctl", 100)

	md := b.Metrics()

	require.Equal(t, 2, md.ResourceMetrics().Len())
	got := attrMap(md.ResourceMetrics().At(1).Resource().Attributes())
	assert.Equal(t, "DRIVE-1", got[AttrSerialNumber])
	assert.Equal(t, "SSD", got[AttrPhysicalDiskType])
	assert.Equal(t, "X9DRW", got[AttrModel], "an empty override keeps the BMC value")
	assert.Equal(t, "pve12", got[ResHostName])
	assert.Equal(t, "S123", attrMap(md.ResourceMetrics().At(0).Resource().Attributes())[AttrSerialNumber], "the BMC resource is unchanged")
}

func TestStatusWithAddsTheExtraAttributesToEveryStatePoint(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.StatusWith(TypeDiskController, "c1", "Ctl", StateDegraded, map[string]string{AttrParent: "s1", AttrModel: "H740", AttrFirmwareVersion: ""})

	dps := find(t, b.Metrics(), MetricStatus).Gauge().DataPoints()

	require.Equal(t, len(States), dps.Len())
	for i := 0; i < dps.Len(); i++ {
		got := attrMap(dps.At(i).Attributes())
		assert.Equal(t, "s1", got[AttrParent])
		assert.Equal(t, "H740", got[AttrModel])
		assert.NotContains(t, got, AttrFirmwareVersion, "an empty extra is left out")
		assert.Equal(t, "disk_controller", got[AttrType])
	}
}

func TestDiskControllerSpeedAndLogicalDiskLimit(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.DiskControllerSpeed("c1", "Ctl", "s1", 12000)
	b.LogicalDiskLimit("v1", "VD", "s1", 5e11, map[string]string{AttrRAIDLevel: "RAID1"})

	md := b.Metrics()

	speed := find(t, md, MetricDiskControllerSpeed)
	assert.Equal(t, "Mbit/s", speed.Unit())
	assert.InDelta(t, 12000, speed.Gauge().DataPoints().At(0).DoubleValue(), 1e-9)
	limit := find(t, md, MetricLogicalDiskLimit)
	assert.Equal(t, "By", limit.Unit())
	assert.Equal(t, "RAID1", attrMap(limit.Gauge().DataPoints().At(0).Attributes())[AttrRAIDLevel])
}

func TestPhysicalDiskIndicatorIsOneWhenOn(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.PhysicalDiskIndicator("d1", "Drive", "s1", true)
	b.PhysicalDiskIndicator("d2", "Drive", "s1", false)

	ind := find(t, b.Metrics(), MetricPhysicalDiskIndicator).Gauge().DataPoints()

	assert.Equal(t, int64(1), ind.At(0).IntValue())
	assert.Equal(t, int64(0), ind.At(1).IntValue())
}

func TestScrapeErrorsIsACumulativeMonotonicSum(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))
	b.ScrapeErrors("drive", 3)

	errs := find(t, b.Metrics(), MetricBMCScrapeErrors)

	assert.Equal(t, "{error}", errs.Unit())
	require.Equal(t, pmetric.MetricTypeSum, errs.Type())
	assert.True(t, errs.Sum().IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, errs.Sum().AggregationTemporality())
	assert.Equal(t, int64(3), errs.Sum().DataPoints().At(0).IntValue())
	assert.Equal(t, "drive", attrMap(errs.Sum().DataPoints().At(0).Attributes())[AttrPathGroup])
}

func TestBuilderAddsTheSpanCountDriveCountAndPredictedFailureGauges(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))

	b.LogicalDiskMediaSpanCount("v1", "VD", "s1", 2)
	b.StorageDriveCount("storage/s1", "Storage s1", 6)
	b.PhysicalDiskPredictedFailure("d1", "Drive", "s1", true)
	b.PhysicalDiskPredictedFailure("d2", "Drive", "s1", false)
	md := b.Metrics()

	span := find(t, md, MetricLogicalDiskMediaSpanCount)
	assert.Equal(t, "hw.logical_disk.media_span_count", span.Name())
	assert.Equal(t, "{span}", span.Unit())
	assert.InDelta(t, 2, span.Gauge().DataPoints().At(0).DoubleValue(), 1e-9)
	count := find(t, md, MetricStorageDriveCount)
	assert.Equal(t, "{drive}", count.Unit())
	assert.InDelta(t, 6, count.Gauge().DataPoints().At(0).DoubleValue(), 1e-9)
	pf := find(t, md, MetricPhysicalDiskPredictedFailure).Gauge().DataPoints()
	assert.Equal(t, int64(1), pf.At(0).IntValue())
	assert.Equal(t, int64(0), pf.At(1).IntValue())
}

func TestFRUInfoIsAnInfoGaugeWithTheTypeAndTheFields(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))

	b.FRUInfo(FRUBoard, map[string]string{AttrID: "fru/0", AttrVendor: "Maker", AttrSerialNumber: "SN1", AttrModel: ""})

	m := find(t, b.Metrics(), MetricBMCFRUInfo)
	assert.Equal(t, "1", m.Unit())
	require.Equal(t, 1, m.Gauge().DataPoints().Len())
	dp := m.Gauge().DataPoints().At(0)
	assert.Equal(t, int64(1), dp.IntValue())
	assert.Equal(t, map[string]string{"fru.type": "board", "hw.id": "fru/0", "hw.vendor": "Maker", "hw.serial_number": "SN1"}, attrMap(dp.Attributes()))
}

func TestInventoryStatusIsOneWhenReadAndZeroWhenNot(t *testing.T) {
	b := NewBuilder("scope", testResource(), time.Unix(100, 0))

	b.InventoryStatus(SourceFRU, true)
	b.InventoryStatus(SourceFRU, false)

	dps := find(t, b.Metrics(), MetricInventoryStatus).Gauge().DataPoints()
	require.Equal(t, 2, dps.Len())
	assert.Equal(t, int64(1), dps.At(0).IntValue())
	assert.Equal(t, int64(0), dps.At(1).IntValue())
	assert.Equal(t, map[string]string{"inventory.source": "fru"}, attrMap(dps.At(0).Attributes()))
}
