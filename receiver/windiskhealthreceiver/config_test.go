// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_TheDefaultsAre5m5sAnd64(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.Equal(t, 5*time.Minute, cfg.CollectionInterval)
	assert.Equal(t, 5*time.Second, cfg.CommandTimeout)
	assert.Equal(t, 64, cfg.MaxDevices)
	assert.Empty(t, cfg.Include)
	assert.Empty(t, cfg.Exclude)
	assert.NoError(t, cfg.Validate())
}

func TestConfig_Validate(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"max_devices of 0":           {func(c *Config) { c.MaxDevices = 0 }, "max_devices must be positive"},
		"negative max_devices":       {func(c *Config) { c.MaxDevices = -1 }, "max_devices must be positive"},
		"interval under a second":    {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"zero interval":              {func(c *Config) { c.CollectionInterval = 0 }, "at least 1 second"},
		"zero timeout":               {func(c *Config) { c.CommandTimeout = 0 }, "command_timeout must be positive"},
		"timeout above the interval": {func(c *Config) { c.CollectionInterval = 10 * time.Second; c.CommandTimeout = 11 * time.Second }, "must not exceed"},
		"bad include expression":     {func(c *Config) { c.Include = []string{"("} }, "include[0]"},
		"bad exclude expression":     {func(c *Config) { c.Exclude = []string{"ok", "["} }, "exclude[1]"},
	}
	for name, c := range cases {
		cfg := createDefaultConfig().(*Config)
		c.mutate(cfg)
		err := cfg.Validate()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

func TestSelector_BadIncludeExpression_ErrorNamesTheFieldAndIndex(t *testing.T) {
	_, err := newSelector([]string{"("}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "include[0]")
	assert.Contains(t, err.Error(), "missing closing )")
}

func TestSelector_BadExcludeExpression_ErrorNamesTheFieldAndIndex(t *testing.T) {
	_, err := newSelector(nil, []string{"ok", "["})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exclude[1]")
	assert.Contains(t, err.Error(), "missing closing ]")
}
