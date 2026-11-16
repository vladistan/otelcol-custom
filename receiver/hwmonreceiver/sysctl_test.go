// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeSysctl answers the named OIDs; any other name is not found.
type fakeSysctl map[string]uint32

func (f fakeSysctl) uint32(name string) (uint32, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("sysctl %s: %w", name, fs.ErrNotExist)
}

// flakySysctl fails the named OIDs with a transient error and answers the rest from the base.
type flakySysctl struct {
	base  fakeSysctl
	fails map[string]error
}

func (f flakySysctl) uint32(name string) (uint32, error) {
	if err, ok := f.fails[name]; ok {
		return 0, err
	}
	return f.base.uint32(name)
}

func newFreeBSDSource(f sysctlReader) *sysctlSource {
	return newSysctlSource(zap.NewNop(), f)
}

func TestSysctlACPIZoneIsTenthsOfAKelvin(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231})

	got, err := src.read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, kindTemperature, got[0].kind)
	assert.Equal(t, "acpi_tz0", got[0].id)
	assert.Equal(t, "acpi_tz0", got[0].name)
	assert.Equal(t, "acpi", got[0].parent)
	assert.InDelta(t, 49.95, got[0].value, 1e-9)
}

func TestSysctlCPUTemperaturesNeedTheSwitch(t *testing.T) {
	sys := fakeSysctl{"hw.ncpu": 2, "dev.cpu.0.temperature": 3181, "dev.cpu.1.temperature": 3191}

	_, err := newFreeBSDSource(sys).read(allOn())
	assert.ErrorIs(t, err, errNoSensors)

	on := allOn()
	on.cpuCores = true
	got, err := newFreeBSDSource(sys).read(on)
	require.NoError(t, err)
	ids := byID(got)
	require.Len(t, ids, 2)
	assert.InDelta(t, 44.95, ids["cpu0"].value, 1e-9)
	assert.InDelta(t, 45.95, ids["cpu1"].value, 1e-9)
	assert.Equal(t, "cpu", ids["cpu0"].parent)
}

func TestSysctlACPIZonesIgnoreTheThermalZonesSwitch(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231})
	off := allOn()
	off.thermalZones = false

	got, err := src.read(off)

	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestSysctlACPILimitsEmitOnlyTheOnesThatExist(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231, "hw.acpi.thermal.tz0._CRT": 3582})

	got, err := src.read(allOn())

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].limits, 1)
	assert.Equal(t, "critical", got[0].limits[0].limitType)
	assert.InDelta(t, 85.05, got[0].limits[0].celsius, 1e-9)
}

func TestSysctlACPILimitTypes(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{
		"hw.acpi.thermal.tz0.temperature": 3231,
		"hw.acpi.thermal.tz0._CRT":        3582, "hw.acpi.thermal.tz0._PSV": 3432, "hw.acpi.thermal.tz0._HOT": 3532,
	})

	got, err := src.read(allOn())

	require.NoError(t, err)
	types := map[string]float64{}
	for _, l := range got[0].limits {
		types[l.limitType] = l.celsius
	}
	assert.InDeltaMapValues(t, map[string]float64{"critical": 85.05, "throttled": 70.05, "high.degraded": 80.05}, types, 1e-9)
}

func TestSysctlLimitsSwitchOffEmitsNone(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231, "hw.acpi.thermal.tz0._CRT": 3582})
	off := allOn()
	off.limits = false

	got, err := src.read(off)

	require.NoError(t, err)
	assert.Empty(t, got[0].limits)
}

func TestSysctlLimitOfZeroOrLessIsNotALimit(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231, "hw.acpi.thermal.tz0._CRT": 0})

	got, err := src.read(allOn())

	require.NoError(t, err)
	assert.Empty(t, got[0].limits)
}

func TestSysctlZonesAreReadUntilTheFirstMissingOne(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231, "hw.acpi.thermal.tz1.temperature": 3241})

	got, err := src.read(allOn())

	require.NoError(t, err)
	assert.Len(t, byID(got), 2)
}

func TestSysctlATransientErrorOnOneZoneDoesNotHideTheNextOnes(t *testing.T) {
	sys := flakySysctl{
		base:  fakeSysctl{"hw.acpi.thermal.tz1.temperature": 3241, "hw.acpi.thermal.tz2.temperature": 3251},
		fails: map[string]error{"hw.acpi.thermal.tz0.temperature": errors.New("input/output error")},
	}

	got, err := newFreeBSDSource(sys).read(allOn())

	require.NoError(t, err)
	ids := byID(got)
	require.Len(t, ids, 2)
	assert.Contains(t, ids, "acpi_tz1")
	assert.Contains(t, ids, "acpi_tz2")
}

func TestSysctlHasNoFans(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 3231})

	got, err := src.read(allOn())

	require.NoError(t, err)
	for _, r := range got {
		assert.NotEqual(t, kindFan, r.kind)
	}
}

func TestSysctlWithNothingToReadGivesNoSensors(t *testing.T) {
	_, err := newFreeBSDSource(fakeSysctl{}).read(allOn())

	assert.ErrorIs(t, err, errNoSensors)
}

func TestSysctlACPIZoneWithAnUnreadableTemperatureIsSkipped(t *testing.T) {
	src := newFreeBSDSource(fakeSysctl{"hw.acpi.thermal.tz0.temperature": 0})

	_, err := src.read(allOn())

	assert.ErrorIs(t, err, errNoSensors)
}
