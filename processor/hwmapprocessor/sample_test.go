// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
	"go.yaml.in/yaml/v3"
)

// loadSample reads the hwmap block of the sample scrape config through the collector's own unmarshaler.
func loadSample(t *testing.T) *Config {
	t.Helper()
	raw, err := os.ReadFile("../../test/configs/scrape-hwmap.yaml")
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	block, ok := doc["processors"].(map[string]any)["hwmap"].(map[string]any)
	require.True(t, ok, "the sample has a processors.hwmap block")
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	require.NoError(t, confmap.NewFromStringMap(block).Unmarshal(cfg))
	require.NoError(t, cfg.Validate())
	require.NotEmpty(t, cfg.Rules)
	return cfg
}

// The raw examples are documentation values shaped like the real readings.
func TestSample_RawExamplesGiveTheTargets(t *testing.T) {
	h := newHarness(t, loadSample(t))
	md := build(map[string]string{"host.name": "switch-a"},
		gaugeOf("snmp.sensor.temperature", "Cel", ip(41, "entity.index", "8", "entity.name", "Sensor A")),
		gaugeOf("snmp.sensor.temperature.threshold", "Cel", ip(0, "entity.index", "8", "entity.name", "Sensor A"), ip(90, "entity.index", "9", "entity.name", "Sensor B")),
		gaugeOf("snmp.sensor.state", "1", ip(1, "entity.index", "8", "entity.name", "Sensor A"), ip(5, "entity.index", "9", "entity.name", "Sensor B")),
		gaugeOf("snmp.board.temperature", "Cel", ip(61, "entity.index", "1001"), ip(55, "entity.index", "1002")),
		gaugeOf("snmp.disk.smart", "1", ip(0, "disk.slot", "1", "disk.descr", "Drive 1", "smart", "GOOD"), ip(0, "disk.slot", "2", "disk.descr", "Drive 2", "smart", "Warning"), ip(0, "disk.slot", "3", "disk.descr", "Drive 3", "smart", "Failing")),
		gaugeOf("snmp.disk.temperature.milli", "mCel", ip(45000, "disk.slot", "1", "disk.descr", "Drive 1")),
		gaugeOf("snmp.volume.size", "KiBy", ip(2, "volume.name", "Volume 1")),
		gaugeOf("snmp.lun.used.percent", "%", ip(77, "lun.id", "3", "lun.name", "LUN A")),
		gaugeOf("snmp.poe.port.allocated", "mW", ip(12500, "port.index", "7", "port.name", "Port 7")),
	)

	got := h.run(md)

	temp := named(got, "hw.temperature")
	require.Len(t, temp, 4, "one switch sensor, two board sensors, one disk")
	by := map[string]flat{}
	for _, p := range temp {
		by[p.attrs["hw.name"]] = p
	}
	assert.Equal(t, 41.0, by["Sensor A"].value)
	assert.Equal(t, "temperature", by["Sensor A"].attrs["hw.type"])
	assert.Equal(t, "8", by["Sensor A"].attrs["hw.id"])
	assert.Equal(t, 61.0, by["Board A"].value, "the board sensors take their names from the index map")
	assert.Equal(t, 55.0, by["Board B"].value)
	assert.InDelta(t, 45.0, by["Drive 1"].value, 1e-9)
	assert.Equal(t, "physical_disk", by["Drive 1"].attrs["hw.type"])

	limit := named(got, "hw.temperature.limit")
	require.Len(t, limit, 1, "the threshold of 0 is unset and gives no point")
	assert.Equal(t, 90.0, limit[0].value)
	assert.Equal(t, "high.critical", limit[0].attrs["hw.limit_type"])

	status := named(got, "hw.status")
	assert.Len(t, status, 3+3*3, "sensor A (3), then three disks (3 each); the not-present sensor B gives none")
	// GOOD is listed and keeps no raw text; Warning and Failing are unlisted, so each takes the default
	// state degraded and keeps its text.
	for slot, want := range map[string]struct{ state, raw string }{
		"1": {"ok", ""}, "2": {"degraded", "Warning"}, "3": {"degraded", "Failing"},
	} {
		seen := 0
		for _, p := range status {
			if p.attrs["disk.slot"] != slot {
				continue
			}
			seen++
			assert.Equal(t, want.raw, p.attrs["hw.status.raw"], "slot "+slot)
			if p.attrs["hw.state"] == want.state {
				assert.Equal(t, 1.0, p.value, "slot "+slot)
			} else {
				assert.Equal(t, 0.0, p.value, "slot "+slot)
			}
		}
		assert.Equal(t, 3, seen, "slot "+slot)
	}

	assert.Equal(t, 2048.0, named(got, "hw.logical_disk.limit")[0].value)
	assert.Equal(t, "By", named(got, "hw.logical_disk.limit")[0].unit)
	assert.InDelta(t, 0.77, named(got, "hw.logical_disk.utilization")[0].value, 1e-9)
	assert.Equal(t, "true", named(got, "hw.logical_disk.utilization")[0].attrs["iscsi.lun"])
	poe := named(got, "poe.port.power")[0]
	assert.InDelta(t, 12.5, poe.value, 1e-9)
	assert.Equal(t, "allocated", poe.attrs["poe.measure"])

	assert.Equal(t, "Example", temp[0].res["hw.vendor"])
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "", ""), "every example point found its rule")
}
