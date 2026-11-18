// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestDefaultConfigIsADailyCollection(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, 24*time.Hour, cfg.CollectionInterval)
	assert.Equal(t, 64, cfg.MaxDrives)
	assert.NoError(t, cfg.Validate())
}

func TestConfigKeysAreRead(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, confmap.NewFromStringMap(map[string]any{"collection_interval": "12h", "max_drives": 8}).Unmarshal(cfg))

	assert.Equal(t, 12*time.Hour, cfg.CollectionInterval)
	assert.Equal(t, 8, cfg.MaxDrives)
}

func TestConfigValidation(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"zero interval":     {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval must be positive"},
		"negative interval": {func(c *Config) { c.CollectionInterval = -time.Hour }, "collection_interval must be positive"},
		"short interval":    {func(c *Config) { c.CollectionInterval = time.Second }, "collection_interval must be at least 1 minute"},
		"no drives":         {func(c *Config) { c.MaxDrives = 0 }, "max_drives must be between 1 and 1024"},
		"too many drives":   {func(c *Config) { c.MaxDrives = 5000 }, "max_drives must be between 1 and 1024"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			tt.mutate(cfg)

			assert.EqualError(t, cfg.Validate(), tt.want)
		})
	}
}

func TestFactoryTypeAndStability(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, "inventory", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryRejectsAnInvalidConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = 0

	_, err := NewFactory().CreateMetrics(t.Context(), receivertest.NewNopSettings(component.MustNewType("inventory")), cfg, consumertest.NewNop())

	assert.Error(t, err)
}

func createWithDefaults(t *testing.T) error {
	t.Helper()
	_, err := NewFactory().CreateMetrics(t.Context(), receivertest.NewNopSettings(component.MustNewType("inventory")), createDefaultConfig(), consumertest.NewNop())
	return err
}

func TestFactoryCreatesOnLinuxAndWindows(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("the receiver has a source on Linux and Windows only")
	}

	assert.NoError(t, createWithDefaults(t))
}

func TestFactoryFailsOnOtherSystems(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		t.Skip("the receiver has a source on this system")
	}

	assert.ErrorContains(t, createWithDefaults(t), "Linux and Windows hosts only")
}
