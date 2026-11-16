// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestFactoryTypeAndStability(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, "hwmon", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryDefaultConfig(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)

	assert.Equal(t, time.Minute, cfg.CollectionInterval)
	assert.Equal(t, "/sys/class/hwmon", cfg.HwmonPath)
	assert.Empty(t, cfg.Include)
	assert.Empty(t, cfg.Exclude)
	assert.True(t, cfg.Limits)
	assert.True(t, cfg.Fans)
	assert.True(t, cfg.Voltages)
	assert.True(t, cfg.ThermalZones)
	assert.False(t, cfg.CPUCores)
	require.NoError(t, cfg.Validate())
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
	cfg.CollectionInterval = 500 * time.Millisecond

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, consumertest.NewNop())

	require.Error(t, err)
}
