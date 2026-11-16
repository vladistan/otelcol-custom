// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
)

func TestDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, 5*time.Minute, cfg.CollectionInterval)
	assert.Equal(t, 5*time.Second, cfg.CommandTimeout)
	assert.True(t, cfg.SkipIfStandby)
	assert.Nil(t, cfg.Attributes, "unset means default")
	sel, err := cfg.attributeSelection()
	require.NoError(t, err)
	assert.True(t, sel.wants(194) && !sel.wants(2))
	assert.Equal(t, []string{"normalized", "raw"}, cfg.ValueTypes)
	assert.Equal(t, 64, cfg.MaxDevices)
	assert.Empty(t, cfg.Include)
	assert.Empty(t, cfg.Exclude)
	require.NoError(t, cfg.Validate())
}

func TestConfigLoadsAllEightKeys(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "10m",
		"command_timeout":     "2s",
		"include":             []any{"^nvme[0-9]+$"},
		"exclude":             []any{"^sdb$"},
		"skip_if_standby":     false,
		"attributes":          "all",
		"value_types":         []any{"normalized", "worst", "threshold", "raw"},
		"max_devices":         8,
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 10*time.Minute, cfg.CollectionInterval)
	assert.Equal(t, 2*time.Second, cfg.CommandTimeout)
	assert.Equal(t, []string{"^nvme[0-9]+$"}, cfg.Include)
	assert.Equal(t, []string{"^sdb$"}, cfg.Exclude)
	assert.False(t, cfg.SkipIfStandby)
	assert.Equal(t, "all", cfg.Attributes)
	assert.Equal(t, []string{"normalized", "worst", "threshold", "raw"}, cfg.ValueTypes)
	assert.Equal(t, 8, cfg.MaxDevices)
}

func TestConfigLoadsAnAttributeIDList(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"attributes": []any{5, 9, 194}})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	sel, err := cfg.attributeSelection()
	require.NoError(t, err)
	assert.True(t, sel.wants(5) && sel.wants(9) && sel.wants(194))
	assert.False(t, sel.wants(1))
}

func TestAttributeSelection(t *testing.T) {
	cases := map[string]struct {
		attrs any
		yes   []uint8
		no    []uint8
	}{
		"default": {"default", []uint8{1, 5, 9, 12, 177, 179, 181, 182, 187, 188, 190, 194, 196, 197, 198, 199, 231, 233, 241, 242}, []uint8{2, 3, 4, 250}},
		"all":     {"all", []uint8{1, 2, 250, 255}, nil},
		"list":    {[]any{"7", 250}, []uint8{7, 250}, []uint8{1, 5}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			cfg.Attributes = c.attrs

			sel, err := cfg.attributeSelection()

			require.NoError(t, err)
			for _, id := range c.yes {
				assert.True(t, sel.wants(id), "id %d", id)
			}
			for _, id := range c.no {
				assert.False(t, sel.wants(id), "id %d", id)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"default":                 {func(*Config) {}, ""},
		"zero interval":           {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"interval under a second": {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"zero timeout":            {func(c *Config) { c.CommandTimeout = 0 }, "command_timeout"},
		"timeout over interval":   {func(c *Config) { c.CommandTimeout = 10 * time.Minute }, "command_timeout"},
		"bad include regex":       {func(c *Config) { c.Include = []string{"("} }, "include"},
		"bad exclude regex":       {func(c *Config) { c.Exclude = []string{"[a"} }, "exclude"},
		"empty attribute list":    {func(c *Config) { c.Attributes = []any{} }, "attributes"},
		"negative attribute id":   {func(c *Config) { c.Attributes = []any{-1} }, "attributes"},
		"fractional attribute id": {func(c *Config) { c.Attributes = []any{5.5} }, "attributes"},
		"unknown attribute word":  {func(c *Config) { c.Attributes = "most" }, "attributes"},
		"attribute id too big":    {func(c *Config) { c.Attributes = []any{"256"} }, "attributes"},
		"default mixed with ids":  {func(c *Config) { c.Attributes = []any{"default", "5"} }, "attributes"},
		"empty value types":       {func(c *Config) { c.ValueTypes = nil }, "value_types"},
		"unknown value type":      {func(c *Config) { c.ValueTypes = []string{"best"} }, "value_types"},
		"duplicate value type":    {func(c *Config) { c.ValueTypes = []string{"raw", "raw"} }, "value_types"},
		"zero max devices":        {func(c *Config) { c.MaxDevices = 0 }, "max_devices"},
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

func TestConfigRejectsAnUnknownKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"smartctl_path": "/usr/sbin/smartctl"})

	require.Error(t, cm.Unmarshal(cfg))
}
