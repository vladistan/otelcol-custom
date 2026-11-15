// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestNewFactoryTypeIsZfskstat(t *testing.T) {
	assert.Equal(t, "zfskstat", NewFactory().Type().String())
}

func TestDefaultConfig(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	require.True(t, ok)

	assert.Equal(t, 60*time.Second, cfg.CollectionInterval)
	assert.Equal(t, "/proc/spl/kstat/zfs", cfg.KstatPath)
	assert.Empty(t, cfg.Pools)
	assert.Empty(t, cfg.ExcludePools)
	assert.True(t, cfg.ArcStats)
	assert.NoError(t, cfg.Validate())
}

func TestCreateMetricsReceiverOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	r, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, r)

	_, err = f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)
	_, err = f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.Error(t, err)
}
