// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func allOn() readOptions {
	return readOptions{limits: true, fans: true, voltages: true, thermalZones: true}
}

func newTestSource(f *tree) *sysfsSource {
	return newSysfsSource(zap.NewNop(), f.hwmonPath())
}

func TestSysfsTemperatureIsMillidegreesAsCelsius(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "coretemp", map[string]string{"temp1_input": "45500", "temp1_label": "Package id 0"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, reading{kind: kindTemperature, id: "coretemp/temp1", name: "Package id 0", parent: "coretemp", value: 45.5}, got[0])
}

func TestSysfsSensorWithoutLabelIsNamedAfterTheFileStem(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp2_input": "31000"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "temp2", got[0].name)
	assert.Equal(t, "nvme/temp2", got[0].id)
}

func TestSysfsLimitsFollowTheFourFilesAndTheSwitch(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{
		"temp1_input": "40000", "temp1_max": "84850", "temp1_crit": "89850",
		"temp1_min": "-5000", "temp1_lcrit": "-10000",
	})

	got, err := newTestSource(f).read(allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
	limits := map[string]float64{}
	for _, l := range got[0].limits {
		limits[l.limitType] = l.celsius
	}
	assert.Equal(t, map[string]float64{"max": 84.85, "critical": 89.85, "low.degraded": -5, "low.critical": -10}, limits)

	off := allOn()
	off.limits = false
	got, err = newTestSource(f).read(off)
	require.NoError(t, err)
	assert.Empty(t, got[0].limits)
}

func TestSysfsOnlyTheLimitFilesThatExistAreEmitted(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "40000", "temp1_crit": "89850"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got[0].limits, 1)
	assert.Equal(t, limit{limitType: "critical", celsius: 89.85}, got[0].limits[0])
}

func TestSysfsFansAreRPMAndFollowTheSwitch(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nct6775", map[string]string{"fan1_input": "1200", "fan1_label": "CPU fan"})

	got, err := newTestSource(f).read(allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, reading{kind: kindFan, id: "nct6775/fan1", name: "CPU fan", parent: "nct6775", value: 1200}, got[0])

	off := allOn()
	off.fans = false
	got, err = newTestSource(f).read(off)
	assert.ErrorIs(t, err, errNoSensors)
	assert.Empty(t, got)
}

func TestSysfsVoltagesAreMillivoltsAsVolts(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nct6775", map[string]string{"in0_input": "1200"})

	got, err := newTestSource(f).read(allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, reading{kind: kindVoltage, id: "nct6775/in0", name: "in0", parent: "nct6775", value: 1.2}, got[0])

	off := allOn()
	off.voltages = false
	got, err = newTestSource(f).read(off)
	assert.ErrorIs(t, err, errNoSensors)
	assert.Empty(t, got)
}

func TestSysfsThermalZonesCarryTheZoneTypeAsParent(t *testing.T) {
	f := newTree(t)
	f.write("thermal/thermal_zone0/type", "acpitz")
	f.write("thermal/thermal_zone0/temp", "27800")

	got, err := newTestSource(f).read(allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, reading{kind: kindTemperature, id: "thermal/thermal_zone0", name: "thermal_zone0", parent: "acpitz", value: 27.8}, got[0])

	off := allOn()
	off.thermalZones = false
	_, err = newTestSource(f).read(off)
	assert.ErrorIs(t, err, errNoSensors)
}

func TestSysfsPerCoreSensorsNeedTheSwitch(t *testing.T) {
	f := newTree(t)
	files := map[string]string{"temp1_input": "50000", "temp1_label": "Package id 0"}
	for i, label := range []string{"Core 0", "Core 1", "Core 2", "Core 3"} {
		n := string(rune('2' + i))
		files["temp"+n+"_input"] = "45000"
		files["temp"+n+"_label"] = label
	}
	f.chip("hwmon0", "coretemp", files)

	got, err := newTestSource(f).read(allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Package id 0", got[0].name)

	on := allOn()
	on.cpuCores = true
	got, err = newTestSource(f).read(on)
	require.NoError(t, err)
	assert.Len(t, got, 5)
}

func TestSysfsCoreLabelOnAnotherChipIsKept(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "k10temp", map[string]string{"temp1_input": "50000", "temp1_label": "Core 0"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestSysfsIDsAreStableAcrossScrapes(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "coretemp", map[string]string{"temp1_input": "50000", "temp3_input": "51000"})
	f.chip("hwmon1", "nvme", map[string]string{"temp1_input": "40000"})
	src := newTestSource(f)

	first, err := src.read(allOn())
	require.NoError(t, err)
	second, err := src.read(allOn())
	require.NoError(t, err)

	assert.Equal(t, byID(first), byID(second))
	assert.Contains(t, byID(first), "coretemp/temp3")
	assert.Contains(t, byID(first), "nvme/temp1")
}

func TestSysfsTwoChipsOfOneNameGetThePCIAddressAsDisambiguator(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon1", "nvme", map[string]string{"temp1_input": "40000"})
	f.nvme("hwmon1", "nvme0", "0000:01:00.0")
	f.chip("hwmon2", "nvme", map[string]string{"temp1_input": "41000"})
	f.nvme("hwmon2", "nvme1", "0000:02:00.0")

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	ids := byID(got)
	require.Len(t, ids, 2)
	assert.InDelta(t, 40.0, ids["nvme@0000:01:00.0/temp1"].value, 1e-9)
	assert.InDelta(t, 41.0, ids["nvme@0000:02:00.0/temp1"].value, 1e-9)
	assert.Equal(t, "nvme", ids["nvme@0000:01:00.0/temp1"].parent)
}

// Two NVMe drives, numbered one way in one boot and the other way in the next:
// the class names and the hwmon indexes swap, the PCI addresses do not.
func TestSysfsDisambiguatorIsTheHardwareAddressNotTheClassNameOrIndex(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon1", "nvme", map[string]string{"temp1_input": "40000"})
	f.nvme("hwmon1", "nvme0", "0000:01:00.0")
	f.chip("hwmon2", "nvme", map[string]string{"temp1_input": "41000"})
	f.nvme("hwmon2", "nvme1", "0000:02:00.0")
	before, err := newTestSource(f).read(allOn())
	require.NoError(t, err)

	swapped := newTree(t)
	swapped.chip("hwmon1", "nvme", map[string]string{"temp1_input": "41000"})
	swapped.nvme("hwmon1", "nvme0", "0000:02:00.0")
	swapped.chip("hwmon2", "nvme", map[string]string{"temp1_input": "40000"})
	swapped.nvme("hwmon2", "nvme1", "0000:01:00.0")
	after, err := newTestSource(swapped).read(allOn())
	require.NoError(t, err)

	assert.Equal(t, byID(before), byID(after))
}

func TestSysfsDeviceWithoutAPCIParentUsesTheDeviceName(t *testing.T) {
	f := newTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "devices", "platform", "coretemp.0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "devices", "platform", "coretemp.1"), 0o755))
	f.chip("hwmon0", "coretemp", map[string]string{"temp1_input": "50000"})
	require.NoError(t, os.Symlink("../../../devices/platform/coretemp.0", filepath.Join(f.hwmonPath(), "hwmon0", "device")))
	f.chip("hwmon1", "coretemp", map[string]string{"temp1_input": "51000"})
	require.NoError(t, os.Symlink("../../../devices/platform/coretemp.1", filepath.Join(f.hwmonPath(), "hwmon1", "device")))

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	ids := byID(got)
	assert.Contains(t, ids, "coretemp@coretemp.0/temp1")
	assert.Contains(t, ids, "coretemp@coretemp.1/temp1")
}

func TestSysfsLimitAtAbsoluteZeroIsAPlaceholder(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{
		"temp1_input": "40000", "temp1_max": "84850", "temp1_min": "-273150", "temp1_lcrit": "-273150",
	})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].limits, 1)
	assert.Equal(t, "max", got[0].limits[0].limitType)
}

func TestSysfsLimitBelowZeroCelsiusStaysWhenItIsReal(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "40000", "temp1_min": "-40000"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got[0].limits, 1)
	assert.InDelta(t, -40.0, got[0].limits[0].celsius, 1e-9)
}

func TestSysfsThermalZoneWithAnHwmonTwinIsReportedOnce(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "acpitz", map[string]string{"temp1_input": "27800"})
	f.write("thermal/thermal_zone0/type", "acpitz")
	f.write("thermal/thermal_zone0/temp", "27800")
	f.write("thermal/thermal_zone1/type", "x86_pkg_temp")
	f.write("thermal/thermal_zone1/temp", "45000")

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	ids := byID(got)
	require.Len(t, ids, 2)
	assert.Contains(t, ids, "acpitz/temp1")
	assert.Contains(t, ids, "thermal/thermal_zone1")
	assert.NotContains(t, ids, "thermal/thermal_zone0")
}

func TestSysfsZoneTypeWithHyphensMatchesTheHwmonChipName(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "x86_pkg_temp", map[string]string{"temp1_input": "45000"})
	f.write("thermal/thermal_zone0/type", "x86-pkg-temp")
	f.write("thermal/thermal_zone0/temp", "45000")

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestSysfsUnreadableHwmonDirectoryIsAnErrorNotNoSensors(t *testing.T) {
	f := newTree(t)
	src := newTestSource(f)
	src.readDir = func(string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "open", Path: f.hwmonPath(), Err: syscall.EACCES}
	}

	_, err := src.read(allOn())

	require.Error(t, err)
	assert.NotErrorIs(t, err, errNoSensors)
	assert.ErrorIs(t, err, syscall.EACCES)
}

func TestSysfsUnreadableHwmonDirectoryStillReportsZones(t *testing.T) {
	f := newTree(t)
	f.write("thermal/thermal_zone0/type", "acpitz")
	f.write("thermal/thermal_zone0/temp", "27800")
	src := newTestSource(f)
	inner := src.readDir
	src.readDir = func(p string) ([]os.DirEntry, error) {
		if p == f.hwmonPath() {
			return nil, &os.PathError{Op: "open", Path: p, Err: syscall.EACCES}
		}
		return inner(p)
	}

	got, err := src.read(allOn())

	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestSysfsTwoChipsWithoutADeviceStillGetDistinctIDs(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon1", "nvme", map[string]string{"temp1_input": "40000"})
	f.chip("hwmon2", "nvme", map[string]string{"temp1_input": "41000"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	assert.Len(t, byID(got), 2)
}

// errorOn makes the read of the named files fail like a sleeping sensor does.
func errorOn(errs map[string]error) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if err, ok := errs[filepath.Base(p)]; ok {
			return nil, &os.PathError{Op: "read", Path: p, Err: err}
		}
		return os.ReadFile(p)
	}
}

func TestSysfsSkipsASensorThatFailsWithEIOOrENODATA(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "40000", "temp2_input": "41000", "temp3_input": "42000"})
	core, logs := observer.New(zapcore.DebugLevel)
	src := newSysfsSource(zap.New(core), f.hwmonPath())
	// FreeBSD has no ENODATA, so the second sensor fails with a plain error.
	src.readFile = errorOn(map[string]error{"temp1_input": syscall.EIO, "temp2_input": errors.New("no data available")})

	got, err := src.read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "nvme/temp3", got[0].id)
	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.DebugLevel).Len())
	assert.Zero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestSysfsSkipsAFileThatIsNotANumber(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "hot", "temp2_input": "41000"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "nvme/temp2", got[0].id)
}

func TestSysfsSkipsALimitThatFails(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "40000", "temp1_max": "84850", "temp1_crit": "89850"})
	src := newTestSource(f)
	src.readFile = errorOn(map[string]error{"temp1_max": syscall.EIO})

	got, err := src.read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].limits, 1)
	assert.Equal(t, "critical", got[0].limits[0].limitType)
}

func TestSysfsChipWithoutANameIsSkipped(t *testing.T) {
	f := newTree(t)
	f.write("hwmon/hwmon0/temp1_input", "40000")
	f.chip("hwmon1", "nvme", map[string]string{"temp1_input": "41000"})

	got, err := newTestSource(f).read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "nvme/temp1", got[0].id)
}

func TestSysfsMissingDirectoriesGiveNoSensors(t *testing.T) {
	f := newTree(t)

	got, err := newTestSource(f).read(allOn())

	assert.ErrorIs(t, err, errNoSensors)
	assert.Empty(t, got)
}

func TestSysfsEmptyHwmonDirectoryGivesNoSensors(t *testing.T) {
	f := newTree(t)
	require.NoError(t, os.MkdirAll(f.hwmonPath(), 0o755))

	_, err := newTestSource(f).read(allOn())

	assert.ErrorIs(t, err, errNoSensors)
}

func TestSysfsReadsNothingOutsideTheSensorFiles(t *testing.T) {
	f := newTree(t)
	f.chip("hwmon0", "nvme", map[string]string{"temp1_input": "40000", "temp1_label": "Composite", "update_interval": "1000", "pwm1": "128", "intrusion0_input": "1", "intrusion0_label": "case"})
	src := newTestSource(f)
	var opened []string
	inner := src.readFile
	src.readFile = func(p string) ([]byte, error) {
		opened = append(opened, filepath.Base(p))
		return inner(p)
	}

	got, err := src.read(allOn())

	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.NotContains(t, opened, "intrusion0_input")
	assert.NotContains(t, opened, "update_interval")
	assert.NotContains(t, opened, "pwm1")
}
