// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
)

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"default is valid":        {func(*Config) {}, ""},
		"interval of one second":  {func(c *Config) { c.CollectionInterval = time.Second }, ""},
		"zero interval":           {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"interval under a second": {func(c *Config) { c.CollectionInterval = 500 * time.Millisecond }, "at least 1 second"},
		"empty hwmon path":        {func(c *Config) { c.HwmonPath = "" }, "hwmon_path"},
		"include glob":            {func(c *Config) { c.Include = []string{"nvme:*"} }, ""},
		"bad include glob":        {func(c *Config) { c.Include = []string{"nvme:[a"} }, "include[0]"},
		"bad exclude glob":        {func(c *Config) { c.Exclude = []string{"ok:*", "x:[a"} }, "exclude[1]"},
		"empty include entry":     {func(c *Config) { c.Include = []string{""} }, "include[0]"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			tt.mutate(cfg)

			err := cfg.Validate()

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigLoadsAllNineKeys(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "30s",
		"hwmon_path":          "/host/sys/class/hwmon",
		"include":             []any{"nvme:*", "coretemp:Package*"},
		"exclude":             []any{"nvme:Sensor 2"},
		"limits":              false,
		"fans":                false,
		"voltages":            false,
		"thermal_zones":       false,
		"cpu_cores":           true,
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	assert.Equal(t, "/host/sys/class/hwmon", cfg.HwmonPath)
	assert.Equal(t, []string{"nvme:*", "coretemp:Package*"}, cfg.Include)
	assert.Equal(t, []string{"nvme:Sensor 2"}, cfg.Exclude)
	assert.False(t, cfg.Limits)
	assert.False(t, cfg.Fans)
	assert.False(t, cfg.Voltages)
	assert.False(t, cfg.ThermalZones)
	assert.True(t, cfg.CPUCores)
}

func TestConfigRejectsAnUnknownKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"cpu_core": true})

	err := cm.Unmarshal(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cpu_core")
}

func TestConfigOptionsFollowTheSwitches(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Fans = false
	cfg.CPUCores = true

	opts := cfg.options()

	assert.Equal(t, readOptions{limits: true, fans: false, voltages: true, thermalZones: true, cpuCores: true}, opts)
}
