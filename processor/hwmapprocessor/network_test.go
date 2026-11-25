// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor

package hwmapprocessor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// The tests in this file feed the sample scrape config the readings of the interface, uptime and Wi-Fi
// metrics. Index and name are the attributes the sample declares; every value is a documentation example.

func cumulative(name, unit string, pts ...pt) spec {
	s := gaugeOf(name, unit, pts...)
	s.sum, s.monotonic = true, true
	return s
}

func ifPoint(v int64, index, name string) pt {
	if name == "" {
		return ip(v, "interface.index", index)
	}
	return ip(v, "interface.index", index, "interface", name)
}

// checkInterfaceSums checks the two points of an in and out counter pair.
func checkInterfaceSums(t *testing.T, got []flat, target, unit string, in, out float64) {
	t.Helper()
	pts := named(got, target)
	require.Len(t, pts, 2, target)
	by := map[string]flat{}
	for _, p := range pts {
		by[p.attrs["network.io.direction"]] = p
		assert.Equal(t, unit, p.unit, target)
		assert.Equal(t, pmetric.MetricTypeSum, p.kind, target)
		assert.Equal(t, pmetric.AggregationTemporalityCumulative, p.temporality, target)
		assert.True(t, p.monotonic, target)
		assert.True(t, p.isInt, target+": an unscaled integer stays an integer")
		assert.Equal(t, "network", p.attrs["hw.type"], target)
		assert.Equal(t, "10", p.attrs["hw.id"], target)
		assert.Equal(t, "Gi1/0/1", p.attrs["hw.name"], target)
	}
	assert.Equal(t, in, by["receive"].value, target)
	assert.Equal(t, out, by["transmit"].value, target)
}

func TestNetwork_ByteFixturesGiveCumulativeSumsWithTheDirectionAndTheInterfaceLabels(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil,
		cumulative("snmp.interface.in.bytes", "By", ifPoint(1200000000000, "10", "Gi1/0/1")),
		cumulative("snmp.interface.out.bytes", "By", ifPoint(900000000000, "10", "Gi1/0/1"))))

	checkInterfaceSums(t, got, "hw.network.io", "By", 1200000000000, 900000000000)
	assert.Empty(t, named(got, "snmp.interface.in.bytes"))
	assert.Empty(t, named(got, "snmp.interface.out.bytes"))
}

func TestNetwork_ErrorFixturesGiveHwErrorsWithTheDirectionAndTheInterfaceErrorType(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil,
		cumulative("snmp.interface.in.errors", "{error}", ifPoint(7, "10", "Gi1/0/1")),
		cumulative("snmp.interface.out.errors", "{error}", ifPoint(0, "10", "Gi1/0/1"))))

	checkInterfaceSums(t, got, "hw.errors", "{error}", 7, 0)
	for _, p := range named(got, "hw.errors") {
		assert.Equal(t, "interface", p.attrs["error.type"])
	}
}

func TestNetwork_PacketFixturesGiveHwNetworkPacketsWithTheDirection(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil,
		cumulative("snmp.interface.in.packets", "{packet}", ifPoint(5000000, "10", "Gi1/0/1")),
		cumulative("snmp.interface.out.packets", "{packet}", ifPoint(4000000, "10", "Gi1/0/1"))))

	checkInterfaceSums(t, got, "hw.network.packets", "{packet}", 5000000, 4000000)
}

// A source point with no interface name leaves hw.name unset and keeps hw.id.
func TestNetwork_NoInterfaceNameLeavesHwNameUnsetAndKeepsHwId(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil,
		cumulative("snmp.interface.in.bytes", "By", ifPoint(1, "10", "")),
		cumulative("snmp.interface.in.errors", "{error}", ifPoint(1, "10", "")),
		cumulative("snmp.interface.in.packets", "{packet}", ifPoint(1, "10", "")),
		gaugeOf("snmp.interface.oper.status", "1", ifPoint(1, "10", ""))))

	for _, target := range []string{"hw.network.io", "hw.errors", "hw.network.packets", "hw.network.up"} {
		pts := named(got, target)
		require.Len(t, pts, 1, target)
		assert.Equal(t, "10", pts[0].attrs["hw.id"], target)
		assert.Equal(t, "network", pts[0].attrs["hw.type"], target)
		_, has := pts[0].attrs["hw.name"]
		assert.False(t, has, target+" has no hw.name")
	}
}

func TestNetwork_OperStatusOneToSevenGivesOneForUpAndZeroForTheRestWithTheStateName(t *testing.T) {
	h := newHarness(t, loadSample(t))
	var pts []pt
	for i, v := range []int64{1, 2, 3, 4, 5, 6, 7} {
		pts = append(pts, ifPoint(v, string(rune('0'+i+1)), "port"+string(rune('0'+i+1))))
	}

	got := h.run(build(nil, gaugeOf("snmp.interface.oper.status", "1", pts...)))

	up := named(got, "hw.network.up")
	require.Len(t, up, 7)
	names := []string{"up", "down", "testing", "unknown", "dormant", "notPresent", "lowerLayerDown"}
	for i, p := range up {
		assert.Equal(t, names[i], p.attrs["hw.network.oper_state"])
		assert.Equal(t, map[bool]float64{true: 1, false: 0}[i == 0], p.value, names[i])
		assert.Equal(t, "network", p.attrs["hw.type"])
		assert.Equal(t, "port"+string(rune('0'+i+1)), p.attrs["hw.name"])
	}
	assert.Equal(t, int64(0), h.counter(unmappedStatesName, "", ""))
}

func TestNetwork_OperStatusOutsideOneToSevenGivesZeroKeepsTheNumberAndIsCountedOnce(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil, gaugeOf("snmp.interface.oper.status", "1", ifPoint(12, "10", "Gi1/0/1"), ifPoint(12, "11", "Gi1/0/2"))))

	up := named(got, "hw.network.up")
	require.Len(t, up, 2)
	assert.Equal(t, 0.0, up[0].value)
	assert.Equal(t, "12", up[0].attrs["hw.network.oper_state"])
	assert.Equal(t, int64(2), h.counter(unmappedStatesName, "rule", "interface-oper-status"))
	assert.Equal(t, 1, h.logs.Len(), "logged once for the value 12")
}

func TestNetwork_UptimeInCentisecondsGivesSecondsAndTheOldNameIsGone(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(map[string]string{"host.name": "switch-a"},
		gaugeOf("snmp.system.uptime", "cs", ip(1330120636))))

	up := named(got, "system.uptime")
	require.Len(t, up, 1)
	assert.InDelta(t, 13301206.36, up[0].value, 1e-6)
	assert.Equal(t, "s", up[0].unit)
	assert.Equal(t, pmetric.MetricTypeGauge, up[0].kind)
	assert.Empty(t, named(got, "snmp.system.uptime"))
}

// The TimeTicks counter wraps at 2^32 centiseconds; the wrapped reading is emitted as read.
func TestNetwork_UptimeAtTheWrapIsEmittedAsRead(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil, gaugeOf("snmp.system.uptime", "cs", ip(4294967295), ip(12))))

	up := named(got, "system.uptime")
	require.Len(t, up, 2)
	assert.InDelta(t, 42949672.95, up[0].value, 1e-6)
	assert.InDelta(t, 0.12, up[1].value, 1e-9, "after the wrap the value starts again from zero and is not corrected")
}

func TestNetwork_UtilizationInPercentGivesARatioWithUnitOneAndTheNamesStay(t *testing.T) {
	h := newHarness(t, loadSample(t))
	radio := func(v int64) pt { return ip(v, "wifi.radio", "r1") }

	got := h.run(build(nil,
		gaugeOf("wifi.radio.utilization.channel", "%", radio(50)),
		gaugeOf("wifi.radio.utilization.rx", "%", radio(50)),
		gaugeOf("wifi.radio.utilization.tx", "%", radio(50))))

	for _, name := range []string{"wifi.radio.utilization.channel", "wifi.radio.utilization.rx", "wifi.radio.utilization.tx"} {
		pts := named(got, name)
		require.Len(t, pts, 1, name)
		assert.InDelta(t, 0.5, pts[0].value, 1e-9, name)
		assert.Equal(t, "1", pts[0].unit, name)
		assert.Equal(t, "r1", pts[0].attrs["wifi.radio"], name+" keeps its attributes")
	}
}

// A value above 100 percent is a ratio above 1.
func TestNetwork_UtilizationAboveOneHundredPercentIsNotClamped(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil, gaugeOf("wifi.radio.utilization.channel", "%", ip(130))))

	pts := named(got, "wifi.radio.utilization.channel")
	require.Len(t, pts, 1)
	assert.InDelta(t, 1.3, pts[0].value, 1e-9)
}

// The five in-house names have no rule, so each point passes unchanged and is counted as unmapped.
func TestNetwork_TheFiveInHouseNamesPassUnchanged(t *testing.T) {
	h := newHarness(t, loadSample(t))
	input := func() pmetric.Metrics {
		return build(map[string]string{"host.name": "switch-a"},
			gaugeOf("snmp.lldp.neighbor", "1", ip(1, "lldp.port", "3", "lldp.neighbor", "peer-a")),
			gaugeOf("wifi.radio.clients", "{client}", ip(14, "wifi.radio", "r1")),
			gaugeOf("wifi.ssid.clients", "{client}", ip(9, "wifi.ssid", "guest")),
			gaugeOf("wifi.radio.channel", "1", ip(36, "wifi.radio", "r1")),
			gaugeOf("wifi.ap.status", "1", ip(1, "wifi.ap", "ap-1")))
	}

	got := h.run(input())

	assert.Equal(t, flatten(input()), got)
	assert.Equal(t, int64(5), h.counter(unmappedPointsName, "", ""))
}

// No series leaves under both an old and a new name: every old name is gone and the Wi-Fi names that stay
// leave only with the new unit.
func TestNetwork_NoSeriesLeavesUnderAnOldAndANewName(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil,
		cumulative("snmp.interface.in.bytes", "By", ifPoint(1, "10", "a")),
		cumulative("snmp.interface.out.bytes", "By", ifPoint(1, "10", "a")),
		cumulative("snmp.interface.in.errors", "{error}", ifPoint(1, "10", "a")),
		cumulative("snmp.interface.out.errors", "{error}", ifPoint(1, "10", "a")),
		cumulative("snmp.interface.in.packets", "{packet}", ifPoint(1, "10", "a")),
		cumulative("snmp.interface.out.packets", "{packet}", ifPoint(1, "10", "a")),
		gaugeOf("snmp.interface.oper.status", "1", ifPoint(1, "10", "a")),
		gaugeOf("snmp.system.uptime", "cs", ip(100)),
		gaugeOf("wifi.radio.utilization.channel", "%", ip(10)),
		gaugeOf("wifi.radio.utilization.rx", "%", ip(10)),
		gaugeOf("wifi.radio.utilization.tx", "%", ip(10))))

	for _, p := range got {
		assert.False(t, strings.HasPrefix(p.metric, "snmp."), "old name %s is gone", p.metric)
		if strings.HasPrefix(p.metric, "wifi.radio.utilization.") {
			assert.Equal(t, "1", p.unit, p.metric)
		}
	}
	assert.Equal(t, int64(0), h.counter(unmappedPointsName, "", ""), "every reading found its rule")
}

// Only the scrape image runs hwmap, so only a scrape config may define the processor.
func TestConfigs_OnlyScrapeConfigsDefineHwmap(t *testing.T) {
	files, err := filepath.Glob("../../test/configs/*.yaml")
	require.NoError(t, err)
	drafts, err := filepath.Glob("../../test/configs-drafts/*/*.yaml")
	require.NoError(t, err)
	files = append(files, drafts...)
	require.NotEmpty(t, files)
	defined := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		has := false
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "hwmap:") {
				has = true
			}
		}
		if !has {
			continue
		}
		defined++
		assert.True(t, strings.HasPrefix(filepath.Base(f), "scrape"), "%s defines hwmap but is not a scrape config", f)
	}
	assert.GreaterOrEqual(t, defined, 1, "the sample scrape config defines it")
}
