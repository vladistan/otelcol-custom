// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"default is valid":        {func(*Config) {}, ""},
		"zero interval":           {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"interval under a second": {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"empty sysfs path":        {func(c *Config) { c.SysfsPath = "" }, "sysfs_path"},
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

func TestConfigLoadsBothKeys(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"collection_interval": "30s", "sysfs_path": "/host/sys/class"})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	assert.Equal(t, "/host/sys/class", cfg.SysfsPath)
}

func TestConfigRejectsAnUnknownKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"username": "x"})

	err := cm.Unmarshal(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "username")
}

func TestFactoryTypeStabilityAndDefaults(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)

	assert.Equal(t, "iscsi", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
	assert.Equal(t, time.Minute, cfg.CollectionInterval)
	assert.Equal(t, "/sys/class", cfg.SysfsPath)
}

func TestFactoryCreatesMetricsOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	_, logsErr := f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	_, tracesErr := f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())

	assert.Error(t, logsErr)
	assert.Error(t, tracesErr)
}

func TestFactoryRejectsAnInvalidConfig(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.CollectionInterval = time.Millisecond
	withGOOS(t, "linux")

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, consumertest.NewNop())

	require.Error(t, err)
}

func TestFactoryCreatesTheReceiverOnLinux(t *testing.T) {
	f := NewFactory()
	withGOOS(t, "linux")

	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), consumertest.NewNop())

	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestFactoryFailsOffLinux(t *testing.T) {
	f := NewFactory()
	for _, goos := range []string{"freebsd", "windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			withGOOS(t, goos)

			_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), consumertest.NewNop())

			require.Error(t, err)
			assert.Contains(t, err.Error(), "Linux")
		})
	}
}

func withGOOS(t *testing.T, name string) {
	t.Helper()
	old := goos
	goos = name
	t.Cleanup(func() { goos = old })
}
