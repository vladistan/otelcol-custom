// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

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
		"interfaces named":        {func(c *Config) { c.Interfaces = []string{"eth0"} }, ""},
		"zero interval":           {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"interval under a second": {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"no drivers":              {func(c *Config) { c.Drivers = nil }, "drivers"},
		"empty driver name":       {func(c *Config) { c.Drivers = []string{"ena", ""} }, "drivers"},
		"empty interface name":    {func(c *Config) { c.Interfaces = []string{""} }, "interfaces"},
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

func TestConfigHasNoStatsRegexKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"stats_regex": "queue_.*"})

	err := cm.Unmarshal(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stats_regex")
}

func TestConfigUnmarshalsTheDocumentedKeys(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "30s",
		"drivers":             []any{"ena", "gve"},
		"interfaces":          []any{"eth0"},
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	assert.Equal(t, []string{"ena", "gve"}, cfg.Drivers)
	assert.Equal(t, []string{"eth0"}, cfg.Interfaces)
}
