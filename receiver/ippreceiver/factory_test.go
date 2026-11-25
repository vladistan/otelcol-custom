// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

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

	assert.Equal(t, "ipp", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryDefaults(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)

	assert.Equal(t, 60*time.Second, cfg.CollectionInterval)
	assert.Equal(t, 10*time.Second, cfg.Timeout)
	assert.Empty(t, cfg.Printers)
}

func TestFactoryCreatesMetricsOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	_, logsErr := f.CreateLogs(context.Background(), set, validConfig(), consumertest.NewNop())
	_, tracesErr := f.CreateTraces(context.Background(), set, validConfig(), consumertest.NewNop())
	r, metricsErr := f.CreateMetrics(context.Background(), set, validConfig(), consumertest.NewNop())

	assert.Error(t, logsErr)
	assert.Error(t, tracesErr)
	require.NoError(t, metricsErr)
	assert.NotNil(t, r)
}

func TestFactoryRejectsADefaultConfigWithoutPrinters(t *testing.T) {
	f := NewFactory()

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()),
		f.CreateDefaultConfig(), consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "printers must not be empty")
}
