// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	stor = sys + "/Storage"
	raid = stor + "/RAID.Integrated.1-1"
	cpu1 = stor + "/CPU.1"
	vol0 = raid + "/Volumes/Disk.Virtual.0:RAID.Integrated.1-1"
)

func health(h any) obj { return obj{"Health": h, "State": "Enabled"} }

func drivePath(id string) string { return stor + "/Drives/" + id }

// driveBody is a synthetic drive; the serial and WWN are made up.
func driveBody(id string, over obj) obj {
	b := obj{
		"Id": id, "Name": "Drive " + id, "Manufacturer": "Example Storage", "Model": "EX-SSD-960", "Revision": "FW01",
		"SerialNumber": "SYNTH-SN-" + id, "MediaType": "SSD", "Protocol": "SAS", "CapacityBytes": 960197124096,
		"PredictedMediaLifeLeftPercent": 80, "Status": health("OK"), "HotspareType": "None",
		"LocationIndicatorActive": false, "FailurePredicted": false,
		"Identifiers": []obj{{"DurableName": "500000SYNTH-WWN" + id, "DurableNameFormat": "NAA"}},
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func controllerBody(id string) obj {
	return obj{
		"MemberId": id, "Name": "Controller " + id, "Manufacturer": "Example Storage", "Model": "EX-RAID-100",
		"FirmwareVersion": "1.2.3", "SpeedGbps": 12, "Status": health("OK"),
	}
}

func storageBody(id string, drives []string, over obj) obj {
	refs := []obj{}
	for _, d := range drives {
		refs = append(refs, ref(drivePath(d)))
	}
	b := obj{
		"Id": id, "Name": "Storage " + id, "Status": health("OK"), "StorageControllers": []obj{controllerBody(id)},
		"Drives": refs, "Volumes": ref(stor + "/" + id + "/Volumes"),
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func volumeBody(over obj) obj {
	b := obj{
		"Id": "Disk.Virtual.0:RAID.Integrated.1-1", "Name": "Virtual Disk 0", "RAIDType": "RAID1", "VolumeType": "Mirrored",
		"CapacityBytes": 479559942144, "Status": health("OK"),
		"Oem": obj{"Dell": obj{"DellVolume": obj{"MediaSpanCount": 2}}},
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

// healthyStorage gives a RAID card with two drives and one volume, and an NVMe storage with one drive.
func healthyStorage(f *fakeRedfish) {
	f.set(stor, obj{"Members": []obj{ref(raid), ref(cpu1)}})
	f.set(raid, storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0", "Disk.Bay.1"}, nil))
	f.set(cpu1, storageBody("CPU.1", []string{"Disk.Bay.2"}, obj{"Volumes": ref(cpu1 + "/Volumes")}))
	f.set(drivePath("Disk.Bay.0"), driveBody("Disk.Bay.0", nil))
	f.set(drivePath("Disk.Bay.1"), driveBody("Disk.Bay.1", obj{"MediaType": "HDD", "PredictedMediaLifeLeftPercent": nil}))
	f.set(drivePath("Disk.Bay.2"), driveBody("Disk.Bay.2", obj{"Protocol": "PCIe", "LocationIndicatorActive": true}))
	f.set(raid+"/Volumes", obj{"Members": []obj{ref(vol0)}})
	f.set(vol0, volumeBody(nil))
	f.set(cpu1+"/Volumes", obj{"Members": []obj{}})
}

// manyDrives gives one storage with n drives and no volumes.
func manyDrives(f *fakeRedfish, n int) {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("Disk.Bay.%d", i)
		f.set(drivePath(ids[i]), driveBody(ids[i], nil))
	}
	f.set(stor, obj{"Members": []obj{ref(raid)}})
	f.set(raid, storageBody("RAID.Integrated.1-1", ids, obj{"Volumes": nil}))
}

func (r *rig) scrapeStorage(t *testing.T) pmetric.Metrics {
	t.Helper()
	return r.target.scrapeStorage(t.Context())
}

type pt struct {
	res   map[string]string
	attrs map[string]string
	v     float64
}

func attrsOf(m pcommon.Map) map[string]string {
	out := map[string]string{}
	m.Range(func(k string, v pcommon.Value) bool { out[k] = v.AsString(); return true })
	return out
}

// allPts returns the points of a metric over every resource.
func allPts(md pmetric.Metrics, name string) []pt {
	var out []pt
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		res := attrsOf(rm.Resource().Attributes())
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			ms := rm.ScopeMetrics().At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				if m.Name() != name {
					continue
				}
				var dps pmetric.NumberDataPointSlice
				if m.Type() == pmetric.MetricTypeSum {
					dps = m.Sum().DataPoints()
				} else {
					dps = m.Gauge().DataPoints()
				}
				for n := 0; n < dps.Len(); n++ {
					v := dps.At(n).DoubleValue()
					if dps.At(n).ValueType() == pmetric.NumberDataPointValueTypeInt {
						v = float64(dps.At(n).IntValue())
					}
					out = append(out, pt{res: res, attrs: attrsOf(dps.At(n).Attributes()), v: v})
				}
			}
		}
	}
	return out
}

// stateOf returns the current hw.state of the component of a type whose id contains the fragment, "" for none.
func stateOf(md pmetric.Metrics, hwType, fragment string) string {
	for _, p := range allPts(md, "hw.status") {
		if p.attrs["hw.type"] == hwType && strings.Contains(p.attrs["hw.id"], fragment) && p.v == 1 {
			return p.attrs["hw.state"]
		}
	}
	return ""
}

func statusPoints(md pmetric.Metrics, hwType, fragment string) int {
	n := 0
	for _, p := range allPts(md, "hw.status") {
		if p.attrs["hw.type"] == hwType && strings.Contains(p.attrs["hw.id"], fragment) {
			n++
		}
	}
	return n
}

func pointFor(t *testing.T, md pmetric.Metrics, metric, fragment string) pt {
	t.Helper()
	for _, p := range allPts(md, metric) {
		if strings.Contains(p.attrs["hw.id"], fragment) {
			return p
		}
	}
	t.Fatalf("no %s point with an id containing %q", metric, fragment)
	return pt{}
}

func errCount(md pmetric.Metrics, group string) int {
	for _, p := range allPts(md, "hw.bmc.scrape.errors") {
		if p.attrs["path_group"] == group {
			return int(p.v)
		}
	}
	return 0
}

func totalErrors(md pmetric.Metrics) int {
	n := 0
	for _, p := range allPts(md, "hw.bmc.scrape.errors") {
		n += int(p.v)
	}
	return n
}

func TestStorageGivesTheRollupControllerVolumeAndDriveSeries(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "storage", "RAID.Integrated.1-1"))
	assert.Equal(t, "ok", stateOf(md, "storage", "CPU.1"))
	assert.Equal(t, "ok", stateOf(md, "disk_controller", "RAID.Integrated.1-1"))
	assert.Equal(t, "ok", stateOf(md, "logical_disk", "Disk.Virtual.0"))
	for _, d := range []string{"Disk.Bay.0", "Disk.Bay.1", "Disk.Bay.2"} {
		assert.Equal(t, "ok", stateOf(md, "physical_disk", d), d)
		assert.Equal(t, len(states()), statusPoints(md, "physical_disk", d), "one point per state")
	}
	assert.Empty(t, allPts(md, "hw.bmc.up"), "a storage read leaves hw.bmc.up to the base scrape")
	assert.Empty(t, allPts(md, "hw.bmc.scrape.duration"))
	assert.Equal(t, 0, totalErrors(md))
}

func states() []string {
	return []string{"ok", "needs_cleaning", "degraded", "predicted_failure", "failed"}
}

func TestControllerSeriesCarryModelFirmwareAndSpeed(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	var ctl pt
	for _, p := range allPts(md, "hw.status") {
		if p.attrs["hw.type"] == "disk_controller" && strings.Contains(p.attrs["hw.id"], "RAID.Integrated.1-1") {
			ctl = p
		}
	}
	assert.Equal(t, "EX-RAID-100", ctl.attrs["hw.model"])
	assert.Equal(t, "1.2.3", ctl.attrs["hw.firmware_version"])
	assert.Equal(t, "RAID.Integrated.1-1", ctl.attrs["hw.parent"])
	speed := pointFor(t, md, "hw.disk_controller.speed", "RAID.Integrated.1-1")
	assert.InDelta(t, 12000, speed.v, 1e-9, "Gbps times 1000, in Mbit/s")
}

func TestDriveResourceCarriesSerialModelFirmwareMediaAndProtocol(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	size := pointFor(t, md, "hw.physical_disk.size", "Disk.Bay.0")
	assert.InDelta(t, 960197124096, size.v, 1)
	assert.Equal(t, "SYNTH-SN-Disk.Bay.0", size.res["hw.serial_number"])
	assert.Equal(t, "EX-SSD-960", size.res["hw.model"])
	assert.Equal(t, "FW01", size.res["hw.firmware_version"])
	assert.Equal(t, "SSD", size.res["hw.physical_disk.type"])
	assert.Equal(t, "SAS", size.res["hw.physical_disk.protocol"])
	assert.Equal(t, "db-1", size.res["host.name"])
	st := pointFor(t, md, "hw.status", "Disk.Bay.0")
	assert.Equal(t, size.res, st.res, "the status of a drive is on the drive resource")
	hdd := pointFor(t, md, "hw.physical_disk.size", "Disk.Bay.1")
	assert.Equal(t, "HDD", hdd.res["hw.physical_disk.type"])
	assert.Equal(t, "SYNTH-SN-Disk.Bay.1", hdd.res["hw.serial_number"])
}

func TestDriveLifeLeftBecomesTheUsedFractionAndAMissingLifeGivesNoSeries(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	end := pointFor(t, md, "hw.physical_disk.endurance_utilization", "Disk.Bay.0")
	assert.InDelta(t, 0.2, end.v, 1e-9)
	assert.Equal(t, "used", end.attrs["hw.physical_disk.state"])
	for _, p := range allPts(md, "hw.physical_disk.endurance_utilization") {
		assert.NotContains(t, p.attrs["hw.id"], "Disk.Bay.1", "a drive with a null life gives no endurance series")
	}
	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.1"), "and keeps its status")
}

func TestDriveIndicatorIsOneWhenLitAndZeroWhenNot(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(drivePath("Disk.Bay.1"), driveBody("Disk.Bay.1", obj{"LocationIndicatorActive": nil, "IndicatorLED": "Blinking"}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0.0, pointFor(t, md, "hw.physical_disk.indicator", "Disk.Bay.0").v)
	assert.Equal(t, 1.0, pointFor(t, md, "hw.physical_disk.indicator", "Disk.Bay.2").v)
	assert.Equal(t, 1.0, pointFor(t, md, "hw.physical_disk.indicator", "Disk.Bay.1").v, "older firmware gives IndicatorLED")
}

func TestVolumeSeriesCarryCapacityRaidLevelAndSpanCount(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	limit := pointFor(t, md, "hw.logical_disk.limit", "Disk.Virtual.0")
	assert.InDelta(t, 479559942144, limit.v, 1)
	assert.Equal(t, "RAID1", limit.attrs["hw.logical_disk.raid_level"])
	assert.Equal(t, "Mirrored", limit.attrs["hw.logical_disk.volume_type"])
	st := pointFor(t, md, "hw.status", "Disk.Virtual.0")
	assert.Equal(t, "RAID1", st.attrs["hw.logical_disk.raid_level"])
	assert.Equal(t, "RAID.Integrated.1-1", st.attrs["hw.parent"])
	assert.NotContains(t, st.attrs, "hw.logical_disk.media_span_count", "the span count is a series, not an attribute")
	assert.NotContains(t, limit.attrs, "hw.logical_disk.media_span_count")
}

func TestVolumeSpanCountIsANumericSeriesAndAVolumeWithoutOneGivesNone(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	other := raid + "/Volumes/Disk.Virtual.1"
	f.set(raid+"/Volumes", obj{"Members": []obj{ref(vol0), ref(other)}})
	f.set(other, volumeBody(obj{"Id": "Disk.Virtual.1", "Oem": nil}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	span := pointFor(t, md, "hw.logical_disk.media_span_count", "Disk.Virtual.0")
	assert.Equal(t, 2.0, span.v)
	assert.Equal(t, "RAID.Integrated.1-1", span.attrs["hw.parent"])
	assert.Equal(t, 1, len(allPts(md, "hw.logical_disk.media_span_count")), "no span count, no series")
}

func TestEachStorageGivesItsDriveCount(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 2.0, pointFor(t, md, "hw.storage.drive_count", "RAID.Integrated.1-1").v)
	assert.Equal(t, 1.0, pointFor(t, md, "hw.storage.drive_count", "CPU.1").v)
	assert.Equal(t, "storage/CPU.1", pointFor(t, md, "hw.storage.drive_count", "CPU.1").attrs["hw.id"])
}

func TestAStorageWithAnEmptyDriveListGivesACountOfZero(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(cpu1, storageBody("CPU.1", nil, obj{"Volumes": ref(cpu1 + "/Volumes")}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0.0, pointFor(t, md, "hw.storage.drive_count", "CPU.1").v)
}

func TestDriveHealthAndFlagsMapToStates(t *testing.T) {
	tests := map[string]struct {
		over  obj
		state string
	}{
		"OK":                       {obj{}, "ok"},
		"Warning":                  {obj{"Status": health("Warning")}, "degraded"},
		"Critical":                 {obj{"Status": health("Critical")}, "failed"},
		"null health":              {obj{"Status": health(nil)}, ""},
		"unknown health":           {obj{"Status": health("Fine")}, ""},
		"no Status":                {obj{"Status": nil}, ""},
		"status of the wrong type": {obj{"Status": "OK"}, ""},
		"predicted failure":        {obj{"FailurePredicted": true}, "ok"},
		"predicted and warning":    {obj{"FailurePredicted": true, "Status": health("Warning")}, "degraded"},
		"predicted and null":       {obj{"FailurePredicted": true, "Status": health(nil)}, ""},
		"predicted and critical":   {obj{"FailurePredicted": true, "Status": health("Critical")}, "failed"},
		"hot spare":                {obj{"HotspareType": "Global"}, "ok"},
		"rebuilding":               {obj{"Status": health("Warning"), "Operations": []obj{{"OperationName": "Rebuilding"}}}, "degraded"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeRedfish()
			defer f.close()
			healthyStorage(f)
			f.set(drivePath("Disk.Bay.0"), driveBody("Disk.Bay.0", tt.over))
			r := newRig(t, f, nil)

			md := r.scrapeStorage(t)

			assert.Equal(t, tt.state, stateOf(md, "physical_disk", "Disk.Bay.0"))
			if tt.state == "" {
				assert.Equal(t, 0, statusPoints(md, "physical_disk", "Disk.Bay.0"), "no health gives no point")
			}
			assert.InDelta(t, 960197124096, pointFor(t, md, "hw.physical_disk.size", "Disk.Bay.0").v, 1, "the size stays")
		})
	}
}

func TestPredictedFailureIsItsOwnSeriesAndNeverHidesTheHealth(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(drivePath("Disk.Bay.0"), driveBody("Disk.Bay.0", obj{"FailurePredicted": true, "Status": health("Warning")}))
	f.set(drivePath("Disk.Bay.1"), driveBody("Disk.Bay.1", obj{"FailurePredicted": nil}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "degraded", stateOf(md, "physical_disk", "Disk.Bay.0"), "a rebuilding drive stays degraded")
	assert.Equal(t, 1.0, pointFor(t, md, "hw.physical_disk.predicted_failure", "Disk.Bay.0").v)
	assert.Equal(t, 0.0, pointFor(t, md, "hw.physical_disk.predicted_failure", "Disk.Bay.2").v)
	for _, p := range allPts(md, "hw.physical_disk.predicted_failure") {
		assert.NotContains(t, p.attrs["hw.id"], "Disk.Bay.1", "a null flag gives no series")
	}
	for _, p := range allPts(md, "hw.status") {
		if p.attrs["hw.state"] == "predicted_failure" {
			assert.Equal(t, 0.0, p.v, "the receiver no longer sets predicted_failure from the flag")
		}
	}
}

func TestAbsentDriveGivesNothing(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(drivePath("Disk.Bay.0"), driveBody("Disk.Bay.0", obj{"Status": obj{"State": "Absent"}}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0, statusPoints(md, "physical_disk", "Disk.Bay.0"))
	assert.Equal(t, 2, len(allPts(md, "hw.physical_disk.size")))
}

func TestHealthMapsForControllerVolumeAndRollup(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(raid, storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0", "Disk.Bay.1"}, obj{"Status": health("Warning")}))
	body := storageBody("CPU.1", []string{"Disk.Bay.2"}, obj{"Volumes": ref(cpu1 + "/Volumes"), "Status": health(nil)})
	body["StorageControllers"] = []obj{controllerBodyWith("CPU.1", obj{"Status": health("Critical")})}
	f.set(cpu1, body)
	f.set(vol0, volumeBody(obj{"Status": health("Critical")}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "degraded", stateOf(md, "storage", "RAID.Integrated.1-1"))
	assert.Equal(t, "", stateOf(md, "storage", "CPU.1"), "a null rollup health gives no point")
	assert.Equal(t, "failed", stateOf(md, "disk_controller", "CPU.1"))
	assert.Equal(t, "failed", stateOf(md, "logical_disk", "Disk.Virtual.0"))
}

func controllerBodyWith(id string, over obj) obj {
	b := controllerBody(id)
	for k, v := range over {
		b[k] = v
	}
	return b
}

func TestControllersLinkOfNewerFirmwareGivesTheControllerSeries(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	body := storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0", "Disk.Bay.1"}, obj{"Controllers": ref(raid + "/Controllers")})
	delete(body, "StorageControllers")
	f.set(raid, body)
	f.set(raid+"/Controllers", obj{"Members": []obj{ref(raid + "/Controllers/RAID.Integrated.1-1")}})
	f.set(raid+"/Controllers/RAID.Integrated.1-1", obj{
		"Id": "RAID.Integrated.1-1", "Name": "Controller", "Model": "EX-RAID-200", "FirmwareVersion": "9.9.9", "SpeedGbps": 24,
		"Status": health("Warning"),
	})
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "degraded", stateOf(md, "disk_controller", "RAID.Integrated.1-1"))
	assert.InDelta(t, 24000, pointFor(t, md, "hw.disk_controller.speed", "RAID.Integrated.1-1").v, 1e-9)
}

func TestOlderFirmwareWithoutVolumesOrControllersStillGivesDrivesAndTheRollup(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	body := storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0"}, nil)
	delete(body, "Volumes")
	delete(body, "StorageControllers")
	f.set(raid, body)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "storage", "RAID.Integrated.1-1"))
	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.0"))
	assert.Equal(t, "", stateOf(md, "disk_controller", "RAID.Integrated.1-1"))
	assert.Equal(t, 0, statusPoints(md, "logical_disk", ""))
	assert.Equal(t, 0, totalErrors(md))
}

func TestADriveListedInlineIsReadWithoutAnotherRequest(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	body := storageBody("RAID.Integrated.1-1", nil, nil)
	body["Drives"] = []obj{driveBody("Disk.Bay.7", nil)}
	f.set(raid, body)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.7"))
	assert.Equal(t, 0, f.count("GET", drivePath("Disk.Bay.7")))
}

func TestControllerWithoutDrivesKeepsItsStatusAndGivesNoDriveSeries(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(cpu1, storageBody("CPU.1", nil, obj{"Volumes": ref(cpu1 + "/Volumes")}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "disk_controller", "CPU.1"))
	assert.Equal(t, "ok", stateOf(md, "storage", "CPU.1"))
	assert.Equal(t, 0, statusPoints(md, "physical_disk", "Disk.Bay.2"))
	assert.Equal(t, 0, totalErrors(md))
}

func TestEmptyStorageCollectionGivesNothingAndNoError(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.set(stor, obj{"Members": []obj{}})
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0, len(allPts(md, "hw.status")))
	assert.Equal(t, 0, totalErrors(md))
}

func TestDriveWithMissingStatusKeepsSizeAndEndurance(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	body := driveBody("Disk.Bay.0", nil)
	delete(body, "Status")
	f.set(drivePath("Disk.Bay.0"), body)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0, statusPoints(md, "physical_disk", "Disk.Bay.0"))
	assert.InDelta(t, 0.2, pointFor(t, md, "hw.physical_disk.endurance_utilization", "Disk.Bay.0").v, 1e-9)
	assert.Equal(t, 0, totalErrors(md), "a missing field is not a failure")
}

func TestMistypedFieldsAreSkippedTheRestOfTheMemberIsKeptAndOneErrorIsCounted(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(drivePath("Disk.Bay.0"), driveBody("Disk.Bay.0", obj{
		"CapacityBytes": "big", "Name": 5, "PredictedMediaLifeLeftPercent": "high", "FailurePredicted": "no",
	}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.0"))
	for _, p := range allPts(md, "hw.physical_disk.size") {
		assert.NotContains(t, p.attrs["hw.id"], "Disk.Bay.0")
	}
	for _, p := range allPts(md, "hw.physical_disk.endurance_utilization") {
		assert.NotContains(t, p.attrs["hw.id"], "Disk.Bay.0")
	}
	assert.Equal(t, 1, errCount(md, "drive"), "one count for the member")
}

func TestStorageSendsOnlyGetsOnTheStoragePaths(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)
	r.scrapeStorage(t)

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.paths)
	for _, p := range f.paths {
		if p == "POST /redfish/v1/SessionService/Sessions" {
			continue
		}
		assert.True(t, strings.HasPrefix(p, "GET "+stor), p)
	}
}

func TestALinkOutsideTheStoragePathsIsNotRequestedAndIsCounted(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.set(raid, storageBody("RAID.Integrated.1-1", nil, obj{
		"Drives": []obj{ref("/redfish/v1/SessionService/Sessions/7"), ref(stor + "/../Managers/x"), ref("https://192.0.2.99" + stor + "/Drives/x")},
	}))
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 0, f.count("GET", "/redfish/v1/SessionService/Sessions/7"))
	assert.Equal(t, 0, f.count("GET", "/redfish/Managers/x"))
	f.mu.Lock()
	for _, p := range f.paths {
		assert.NotContains(t, p, "192.0.2.99")
		assert.NotContains(t, p, "..")
	}
	f.mu.Unlock()
	assert.Equal(t, 3, errCount(md, "drive"))
}

func TestSixDrivesAreAllReportedUnderTheDefaultCap(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 6)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 6, len(allPts(md, "hw.physical_disk.size")))
	assert.Equal(t, 0, totalErrors(md))
}

func TestThirtyDrivesAreAllReportedUnderTheDefaultCap(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 30)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, 30, len(allPts(md, "hw.physical_disk.size")))
	assert.Equal(t, 32*len(states()), len(allPts(md, "hw.status")), "30 drives, the rollup and the controller")
	assert.Equal(t, 0, totalErrors(md))
}

func TestTheCapStopsTheDriveReadsAndCountsOneErrorWithOneWarning(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 30)
	r := newRig(t, f, func(c *TargetConfig) { c.StorageMaxDrives = 24 })

	md := r.scrapeStorage(t)
	md2 := r.scrapeStorage(t)

	assert.Equal(t, 24, len(allPts(md, "hw.physical_disk.size")))
	assert.Equal(t, 1, errCount(md, "drive"))
	assert.Equal(t, 2, errCount(md2, "drive"), "cumulative")
	assert.Equal(t, 0, f.count("GET", drivePath("Disk.Bay.24")), "no request past the cap")
	assert.Equal(t, 2, f.count("GET", drivePath("Disk.Bay.23")))
	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("limit").Len())
}

func TestACapOfSixGivesExactlySixOfSix(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 6)
	r := newRig(t, f, func(c *TargetConfig) { c.StorageMaxDrives = 6 })

	md := r.scrapeStorage(t)

	assert.Equal(t, 6, len(allPts(md, "hw.physical_disk.size")))
	assert.Equal(t, 0, totalErrors(md))
}

func TestAnOversizedCollectionIsReadUpToTheMemberLimit(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	members := []obj{}
	for i := range maxCollectionMembers + 50 {
		members = append(members, volumeBody(obj{"Id": fmt.Sprintf("Disk.Virtual.%d", i), "Name": "v"}))
	}
	f.set(raid+"/Volumes", obj{"Members": members})
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, maxCollectionMembers, len(allPts(md, "hw.logical_disk.limit")))
	assert.Equal(t, 1, errCount(md, "volume"))
}

type shape struct {
	name    string
	arrange func(f *fakeRedfish, path string)
	counts  bool // the shape is a failed read
}

func shapes() []shape {
	return []shape{
		{"401", func(f *fakeRedfish, p string) { f.forceStatus(p, 401) }, true},
		{"403", func(f *fakeRedfish, p string) { f.forceStatus(p, 403) }, true},
		{"404", func(f *fakeRedfish, p string) { f.forceStatus(p, 404) }, true},
		{"405", func(f *fakeRedfish, p string) { f.forceStatus(p, 405) }, true},
		{"500", func(f *fakeRedfish, p string) { f.forceStatus(p, 500) }, true},
		{"timeout", func(f *fakeRedfish, p string) { f.stall[p] = 2 * time.Second }, true},
		{"not JSON", func(f *fakeRedfish, p string) { f.setRaw(p, []byte("<html>oops</html>")) }, true},
		{"truncated", func(f *fakeRedfish, p string) {
			b, _ := json.Marshal(f.routes[p])
			f.setRaw(p, b[:len(b)/2])
		}, true},
		{"wrong type", func(f *fakeRedfish, p string) { f.setRaw(p, []byte(`["a","b"]`)) }, true},
		{"null body", func(f *fakeRedfish, p string) { f.setRaw(p, []byte("null")) }, false},
		{"empty object", func(f *fakeRedfish, p string) { f.setRaw(p, []byte("{}")) }, false},
	}
}

func TestEveryFailureShapeOnEveryStoragePathSkipsThatPathAndKeepsTheRest(t *testing.T) {
	paths := []struct {
		name, path, group string
		whole             bool // the failure removes everything
	}{
		{"collection", stor, "storage", true},
		{"storage member", raid, "storage", false},
		{"drive", drivePath("Disk.Bay.1"), "drive", false},
		{"volumes collection", raid + "/Volumes", "volume", false},
		{"volume member", vol0, "volume", false},
	}
	for _, p := range paths {
		for _, s := range shapes() {
			t.Run(p.name+"/"+s.name, func(t *testing.T) {
				f := newFakeRedfish()
				defer f.close()
				healthyStorage(f)
				s.arrange(f, p.path)
				r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = 100 * time.Millisecond; c.ScrapeTimeout = 20 * time.Second })

				var md pmetric.Metrics
				require.NotPanics(t, func() { md = r.scrapeStorage(t) })

				assert.Empty(t, allPts(md, "hw.bmc.up"), "hw.bmc.up is left alone")
				if !p.whole {
					assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.2"), "the other storage is read")
				}
				if s.counts {
					assert.Equal(t, 1, errCount(md, p.group), "one count")
				} else {
					assert.Equal(t, 0, totalErrors(md))
				}
			})
		}
	}
}

func TestADriveThatFailsDoesNotRemoveItsNeighbours(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(drivePath("Disk.Bay.0"), 500)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.1"))
	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.2"))
	assert.Equal(t, "ok", stateOf(md, "storage", "RAID.Integrated.1-1"))
	assert.Equal(t, "", stateOf(md, "physical_disk", "Disk.Bay.0"))
}

func TestBMCWithoutStorageIsMarkedAbsentWithOneInfoAndCountedOnce(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.remove(stor)
	r := newRig(t, f, nil)

	first := r.scrapeStorage(t)
	second := r.scrapeStorage(t)

	assert.Equal(t, 1, errCount(first, "storage"))
	assert.Equal(t, 1, errCount(second, "storage"), "the remembered 404 is not a new failure")
	assert.Equal(t, 1, f.count("GET", stor), "asked once and not again")
	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("absent").Len())
}

func TestAFailingPathWarnsOncePerEpisodeAndTheCounterIsCumulative(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(drivePath("Disk.Bay.0"), 500)
	r := newRig(t, f, nil)

	a := r.scrapeStorage(t)
	b := r.scrapeStorage(t)
	c := r.scrapeStorage(t)
	f.mu.Lock()
	delete(f.status, drivePath("Disk.Bay.0"))
	f.mu.Unlock()
	d := r.scrapeStorage(t)
	f.forceStatus(drivePath("Disk.Bay.0"), 500)
	e := r.scrapeStorage(t)

	assert.Equal(t, []int{1, 2, 3, 3, 4}, []int{errCount(a, "drive"), errCount(b, "drive"), errCount(c, "drive"), errCount(d, "drive"), errCount(e, "drive")})
	assert.Equal(t, 2, r.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("storage read").Len(), "one for each episode")
}

func TestAnAuthFailureOnAStoragePathDoesNotHaltTheTarget(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(drivePath("Disk.Bay.0"), 401)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)
	base := r.scrape(t)

	assert.Equal(t, 1, errCount(md, "drive"))
	u, _ := up(t, base)
	assert.Equal(t, int64(1), u)
	assert.Empty(t, r.authFails, "a storage 401 is not a login failure")
}

func TestStorageIsNotReadWhileTheTargetIsHaltedOrBackingOff(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.loginStatus = 401
	r := newRig(t, f, nil)
	r.scrape(t)
	logins := f.logins

	md := r.scrapeStorage(t)

	assert.Equal(t, 0, md.DataPointCount())
	assert.Equal(t, logins, f.logins, "no new login attempt")
}

func TestSlowStorageSendsWhatWasReadAndCountsTheTimeout(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.stall[drivePath("Disk.Bay.1")] = 3 * time.Second
	r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = time.Second; c.ScrapeTimeout = 300 * time.Millisecond })
	r.target.drivePace = 0

	start := time.Now()
	var md pmetric.Metrics
	require.NotPanics(t, func() { md = r.scrapeStorage(t) })

	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.0"), "the drive read before the expiry is sent")
	assert.Equal(t, "ok", stateOf(md, "storage", "RAID.Integrated.1-1"))
	assert.Equal(t, 1, errCount(md, "drive"), "one timeout, one count")
}

func TestAReadThatExpiresMidWaySendsPartialPointsAndCountsOneError(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 100)
	for i := range 100 {
		f.stall[drivePath(fmt.Sprintf("Disk.Bay.%d", i))] = 15 * time.Millisecond
	}
	r := newRig(t, f, func(c *TargetConfig) {
		c.RequestTimeout = 200 * time.Millisecond
		c.ScrapeTimeout = 300 * time.Millisecond
	})
	r.target.drivePace = 0

	md := r.scrapeStorage(t)

	sizes := len(allPts(md, "hw.physical_disk.size"))
	assert.Greater(t, sizes, 0)
	assert.Less(t, sizes, 100)
	assert.Equal(t, 1, errCount(md, "drive"))
	assert.Equal(t, 1, totalErrors(md))
}

func TestAHundredDrivesAreAllSentWhenTheReadOutlastsTheScrapeTimeout(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	manyDrives(f, 100)
	for i := range 100 {
		f.stall[drivePath(fmt.Sprintf("Disk.Bay.%d", i))] = 15 * time.Millisecond
	}
	r := newRig(t, f, func(c *TargetConfig) {
		c.RequestTimeout = 200 * time.Millisecond
		c.ScrapeTimeout = 300 * time.Millisecond
	})
	r.target.drivePace = 40 * time.Millisecond

	md := r.scrapeStorage(t)

	assert.Equal(t, 100, len(allPts(md, "hw.physical_disk.size")))
	assert.Equal(t, 0, totalErrors(md))
}

func TestTheReadBudgetHasACeiling(t *testing.T) {
	cfg := TargetConfig{ScrapeTimeout: 30 * time.Second}
	tg := &target{cfg: cfg, drivePace: time.Second, readCeiling: 2 * time.Minute}

	assert.Equal(t, 30*time.Second, tg.readBudget(0))
	assert.Equal(t, 40*time.Second, tg.readBudget(10))
	assert.Equal(t, 2*time.Minute, tg.readBudget(1000))
	tg.cfg.ScrapeTimeout = 5 * time.Minute
	assert.Equal(t, 5*time.Minute, tg.readBudget(1000), "a scrape_timeout above the ceiling is kept")
}

func TestAnExpiryBetweenTwoDriveRequestsIsCounted(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)
	r.target.storage.errors, r.target.storage.warned = map[string]int64{}, map[string]bool{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run := &storageRun{t: r.target, ctx: ctx, parent: t.Context(), failed: map[string]bool{}}

	run.readDrives(storageData{id: "RAID.Integrated.1-1", drives: []driveEntry{{link: drivePath("Disk.Bay.0")}, {link: drivePath("Disk.Bay.1")}}})

	assert.Equal(t, int64(1), r.target.storage.errors["drive"])
	assert.Equal(t, 0, f.count("GET", drivePath("Disk.Bay.0")), "no request after the expiry")
}

func TestAShutdownIsNotCountedAsATimeout(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)
	r.target.storage.errors, r.target.storage.warned = map[string]int64{}, map[string]bool{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run := &storageRun{t: r.target, ctx: ctx, parent: ctx, failed: map[string]bool{}}

	run.readDrives(storageData{id: "x", drives: []driveEntry{{link: drivePath("Disk.Bay.0")}}})

	assert.Equal(t, int64(0), r.target.storage.errors["drive"])
}

func TestADriveThatAnswered404IsReadAgainOnTheNextRead(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(drivePath("Disk.Bay.0"), 404)
	r := newRig(t, f, nil)

	first := r.scrapeStorage(t)
	f.mu.Lock()
	delete(f.status, drivePath("Disk.Bay.0"))
	f.mu.Unlock()
	second := r.scrapeStorage(t)

	assert.Equal(t, "", stateOf(first, "physical_disk", "Disk.Bay.0"))
	assert.Equal(t, 1, errCount(first, "drive"))
	assert.Equal(t, "ok", stateOf(second, "physical_disk", "Disk.Bay.0"), "the drive is back on the next read")
	assert.Equal(t, 1, errCount(second, "drive"), "no new failure")
}

func TestAMemberThatKeepsAnswering404IsAskedEachReadAndCountedOnce(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(vol0, 404)
	r := newRig(t, f, nil)

	r.scrapeStorage(t)
	r.scrapeStorage(t)
	md := r.scrapeStorage(t)

	assert.Equal(t, 3, f.count("GET", vol0))
	assert.Equal(t, 1, errCount(md, "volume"))
}

func TestAMissingStorageCollectionIsAskedAgainAfterAnHourNotSixHours(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.remove(stor)
	r := newRig(t, f, nil)

	r.scrapeStorage(t)
	r.clock.Advance(30 * time.Minute)
	r.scrapeStorage(t)
	assert.Equal(t, 1, f.count("GET", stor), "still marked absent")
	r.clock.Advance(31 * time.Minute)
	r.scrapeStorage(t)

	assert.Equal(t, 2, f.count("GET", stor))
}

func TestEveryErrorGroupHasACounterFromTheFirstRead(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)

	groups := map[string]float64{}
	for _, p := range allPts(md, "hw.bmc.scrape.errors") {
		groups[p.attrs["path_group"]] = p.v
	}
	assert.Equal(t, map[string]float64{"storage": 0, "controller": 0, "drive": 0, "volume": 0}, groups)
}

func TestATimeoutBeforeTheVolumesDoesNotResetTheirWarning(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(vol0, 403)
	r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = time.Second; c.ScrapeTimeout = 300 * time.Millisecond })
	r.target.drivePace = 0
	volumeWarnings := func() int {
		return r.logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("part", "volume")).Len()
	}

	r.scrapeStorage(t)
	f.mu.Lock()
	f.stall[drivePath("Disk.Bay.0")] = 2 * time.Second
	f.mu.Unlock()
	r.scrapeStorage(t)
	f.mu.Lock()
	delete(f.stall, drivePath("Disk.Bay.0"))
	f.mu.Unlock()
	r.scrapeStorage(t)

	assert.Equal(t, 1, volumeWarnings(), "the volumes were not read in the second run, so the episode goes on")
}

func TestParseStorageTellsLinksFromInlineDrives(t *testing.T) {
	link := "/redfish/v1/Systems/S/Storage/R/Drives/Disk.Bay.0"
	tests := map[string]struct {
		entry         string
		links, inline int
		dropped       int
	}{
		"plain link":          {`{"@odata.id":"` + link + `"}`, 1, 0, 0},
		"annotated link":      {`{"@odata.id":"` + link + `","@odata.type":"#Drive.Drive","@odata.count":1}`, 1, 0, 0},
		"inline drive":        {`{"Id":"d","Status":{"Health":"OK"}}`, 0, 1, 0},
		"inline with id":      {`{"@odata.id":"` + link + `","@odata.type":"#Drive.Drive","Model":"X"}`, 0, 1, 0},
		"inline of one field": {`{"CapacityBytes":5}`, 0, 1, 0},
		"unusable":            {`{"Foo":1}`, 0, 0, 1},
		"empty":               {`{}`, 0, 0, 1},
		"null":                {`null`, 0, 0, 1},
		"annotation only":     {`{"@odata.type":"#Drive.Drive"}`, 0, 0, 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s, _ := parseStorage([]byte(`{"Id":"R","Drives":[` + tt.entry + `]}`))

			links, inline := 0, 0
			for _, e := range s.drives {
				if e.link != "" {
					links++
				}
				if e.inline != nil {
					inline++
				}
			}
			assert.Equal(t, tt.links, links)
			assert.Equal(t, tt.inline, inline)
			assert.Equal(t, tt.dropped, s.dropped)
		})
	}
}

func TestAnnotatedDriveLinksAreFetchedMixedWithInlineDrivesAndAnUnusableEntryIsCounted(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	body := storageBody("RAID.Integrated.1-1", nil, nil)
	annotated := ref(drivePath("Disk.Bay.0"))
	annotated["@odata.type"] = "#Drive.Drive"
	body["Drives"] = []obj{annotated, driveBody("Disk.Bay.7", nil), {"Foo": 1}, {"Foo": 2}}
	f.set(raid, body)
	r := newRig(t, f, nil)

	md := r.scrapeStorage(t)
	r.scrapeStorage(t)

	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.0"), "the annotated link is fetched")
	assert.Equal(t, 2, f.count("GET", drivePath("Disk.Bay.0")), "fetched on each read")
	assert.Equal(t, "ok", stateOf(md, "physical_disk", "Disk.Bay.7"))
	assert.Equal(t, 2, errCount(md, "drive"), "one count for each unusable entry")
	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("part", "drive")).Len(), "one warning")
}

func TestBothControllerLayoutsGiveTheSameIDForTheSameController(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	inline := storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0"}, nil)
	inline["StorageControllers"] = []obj{{"MemberId": "0", "Name": "PERC", "Model": "EX-RAID-100", "Status": health("OK")}}
	f.set(raid, inline)
	r := newRig(t, f, nil)
	a := r.scrapeStorage(t)

	linked := storageBody("RAID.Integrated.1-1", []string{"Disk.Bay.0"}, obj{"Controllers": ref(raid + "/Controllers")})
	delete(linked, "StorageControllers")
	f.set(raid, linked)
	f.set(raid+"/Controllers", obj{"Members": []obj{ref(raid + "/Controllers/RAID.Integrated.1-1")}})
	f.set(raid+"/Controllers/RAID.Integrated.1-1", obj{"Id": "RAID.Integrated.1-1", "Name": "PERC", "Model": "EX-RAID-100", "Status": health("OK")})
	b := r.scrapeStorage(t)

	idOf := func(md pmetric.Metrics) string {
		for _, p := range allPts(md, "hw.status") {
			if p.attrs["hw.type"] == "disk_controller" && strings.Contains(p.attrs["hw.id"], "RAID.Integrated.1-1") {
				return p.attrs["hw.id"]
			}
		}
		return ""
	}
	assert.Equal(t, "disk_controller/RAID.Integrated.1-1/RAID.Integrated.1-1", idOf(a))
	assert.Equal(t, idOf(a), idOf(b))
}

func TestControllerIDFallsBackToMemberIDAndIndexOnlyWithoutAnID(t *testing.T) {
	two := `{"Id":"S","StorageControllers":[{"MemberId":"A"},{"Id":"Ctl.1"},{}]}`
	s, _ := parseStorage([]byte(two))
	require.Len(t, s.controllers, 3)
	assert.Equal(t, "A", s.controllers[0].id, "no Id: the MemberId")
	assert.Equal(t, "Ctl.1", s.controllers[1].id, "the Id wins")
	assert.Equal(t, "2", s.controllers[2].id, "no Id and no MemberId: the index")

	one, _ := parseStorage([]byte(`{"Id":"S","StorageControllers":[{"MemberId":"0"}]}`))
	assert.Equal(t, "S", one.controllers[0].id, "a lone controller without an Id is the storage")
	both, _ := parseStorage([]byte(`{"Id":"S","StorageControllers":[{"Id":"Own","MemberId":"0"}]}`))
	assert.Equal(t, "Own", both.controllers[0].id)
	c, _ := parseController([]byte(`{"MemberId":"7"}`))
	assert.Equal(t, "7", c.id)
}

func TestAPanicInADecoderIsRecoveredWithOneErrorPerTargetAndTheNextReadRuns(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)
	r.target.onDecode = func(group string) {
		if group == "drive" {
			panic("synthetic decoder failure")
		}
	}

	var first, second pmetric.Metrics
	require.NotPanics(t, func() { first = r.scrapeStorage(t) })
	require.NotPanics(t, func() { second = r.scrapeStorage(t) })

	assert.Empty(t, allPts(first, "hw.bmc.up"), "a storage panic leaves hw.bmc.up to the base scrape")
	assert.Empty(t, allPts(second, "hw.bmc.up"))
	assert.Equal(t, 1, errCount(first, "drive"))
	assert.Equal(t, 2, errCount(second, "drive"), "cumulative")
	assert.Equal(t, 4, len(allPts(first, "hw.bmc.scrape.errors")), "every group keeps its series")
	assert.Equal(t, 0, len(allPts(first, "hw.status")), "no health point from a read that panicked")
	errs := r.logs.FilterLevelExact(zapcore.ErrorLevel)
	assert.Equal(t, 1, errs.Len(), "one ERROR per target")
	assert.Equal(t, "drive", errs.All()[0].ContextMap()["part"])

	r.target.onDecode = nil
	third := r.scrapeStorage(t)
	assert.Empty(t, allPts(third, "hw.bmc.up"))
	assert.Equal(t, "ok", stateOf(third, "physical_disk", "Disk.Bay.0"))
}

func TestNoSerialWWNPasswordOrTokenReachesALog(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	f.forceStatus(drivePath("Disk.Bay.0"), 500)
	f.setRaw(drivePath("Disk.Bay.1"), []byte(`{"SerialNumber": SYNTH-SN-Disk.Bay.1 "Identifiers": 500000SYNTH-WWN`))
	f.forceStatus(vol0, 403)
	r := newRig(t, f, nil)

	r.scrapeStorage(t)
	r.scrapeStorage(t)

	require.NotZero(t, r.logs.Len())
	for _, e := range r.logs.All() {
		text := e.Message
		for k, v := range e.ContextMap() {
			text += " " + k + "=" + fmt.Sprint(v)
		}
		for _, secret := range []string{"SYNTH-SN", "SYNTH-WWN", sessionToken, "secret"} { // pragma: allowlist secret
			assert.NotContains(t, text, secret)
		}
	}
}

func TestRandomBodiesOnEveryPathNeverPanic(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	healthyStorage(f)
	r := newRig(t, f, nil)
	var paths []string
	f.mu.Lock()
	for p := range f.routes {
		paths = append(paths, p)
	}
	f.mu.Unlock()
	bodies := []string{
		"", "null", "[]", "{}", `"x"`, "1", `{"Members": null}`, `{"Members": [null, 1, "x", {}, []]}`, `{"Members": {"a": 1}}`,
		`{"Drives": "x", "Volumes": 3, "StorageControllers": {"a": 1}}`, `{"Drives": [null, 1, {"@odata.id": 5}], "Status": []}`,
		`{"Status": {"Health": 3, "State": []}, "CapacityBytes": [], "Id": {}}`, `{"Temperatures": 1, "Fans": "x", "PowerSupplies": {}}`,
		`{"Members": [{"@odata.id": ""}, {"@odata.id": null}]}`, strings.Repeat("[", 5000),
	}
	for _, p := range paths {
		for _, b := range bodies {
			f.setRaw(p, []byte(b))
			require.NotPanics(t, func() { r.scrape(t); r.scrapeStorage(t) }, "%s with %q", p, b)
		}
		f.mu.Lock()
		delete(f.raw, p)
		f.mu.Unlock()
	}

	assert.Zero(t, r.logs.FilterLevelExact(zapcore.ErrorLevel).Len(), "nothing needed the recovery")
}

func FuzzStorageDecoders(f *testing.F) {
	seeds := []string{
		`{"Id":"a","Status":{"Health":"OK"},"CapacityBytes":1,"Drives":[{"@odata.id":"/x"}]}`, "null", "[]", `{"Members":[1]}`,
		`{"StorageControllers":[{"SpeedGbps":"x"}],"Volumes":{"@odata.id":1}}`, `{"Oem":{"Dell":{"DellVolume":[]}}}`, "{",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		parseStorage(data)
		parseDrive(data)
		parseVolume(data)
		parseController(data)
	})
}

func TestStorageDecodersAcceptTheShapesOfAnIDRAC(t *testing.T) {
	d, bad := parseDrive(mustJSON(driveBody("Disk.Bay.0", nil)))

	assert.Zero(t, bad)
	assert.Equal(t, "Disk.Bay.0", d.id)
	assert.Equal(t, "SYNTH-SN-Disk.Bay.0", d.serial)
	require.NotNil(t, d.capacity)
	assert.InDelta(t, 960197124096, *d.capacity, 1)
	require.NotNil(t, d.health)
	assert.Equal(t, "OK", *d.health)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func storageConfig(f *fakeRedfish, collection, storage time.Duration, resources []string) *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = collection
	tc := validTarget()
	tc.Address = f.srv.URL
	tc.InsecureSkipVerify = true
	tc.StorageInterval = storage
	tc.Resources = resources
	cfg.Targets = []TargetConfig{tc}
	return cfg
}

// batches splits what the sink got into base batches (they carry hw.bmc.scrape.duration) and storage batches.
func batches(sink interface{ AllMetrics() []pmetric.Metrics }) (base, storage int) {
	for _, md := range sink.AllMetrics() {
		if len(allPts(md, "hw.bmc.scrape.duration")) > 0 {
			base++
		} else if len(allPts(md, "hw.status")) > 0 {
			storage++
		}
	}
	return base, storage
}

func TestABasePanicIsRecoveredWithOneErrorAndTheScrapeReportsDown(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, nil)
	r.target.onDecode = func(part string) {
		if part == "thermal" {
			panic("synthetic decoder failure")
		}
	}

	var first, second pmetric.Metrics
	require.NotPanics(t, func() { first = r.scrape(t) })
	require.NotPanics(t, func() { second = r.scrape(t) })

	_, reason := up(t, first)
	assert.Equal(t, "error", reason)
	_, reason = up(t, second)
	assert.Equal(t, "error", reason)
	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.ErrorLevel).Len())
	r.target.onDecode = nil
	u, _ := up(t, r.scrape(t))
	assert.Equal(t, int64(1), u, "the next scrape runs")
}

func TestAMemberLinkThatAnswers500IsSkippedInTheBaseScrapeWithOneWarning(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.set(sys+"/Memory", obj{"Members": []obj{ref(sys + "/Memory/DIMM.Socket.A1"), ref(sys + "/Memory/DIMM.Socket.A2")}})
	f.set(sys+"/Memory/DIMM.Socket.A2", obj{"Id": "DIMM.Socket.A2", "Name": "DIMM A2", "CapacityMiB": 8192, "Status": obj{"Health": "OK", "State": "Enabled"}})
	f.forceStatus(sys+"/Memory/DIMM.Socket.A1", 500)
	r := newRig(t, f, nil)

	md := r.scrape(t)
	r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 1, pointCount(md, "hw.memory.size"))
	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("part", "member")).Len())
}

func TestAnAbsentErrorReadsAsPathAbsent(t *testing.T) {
	assert.EqualError(t, &absentError{}, "path absent")
}
