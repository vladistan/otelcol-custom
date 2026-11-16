// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestFactoryTypeAndStability(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, component.MustNewType("smart"), f.Type())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryDefaultConfigIsValid(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig()

	require.NoError(t, componentValidate(cfg))
}

func componentValidate(cfg component.Config) error {
	return cfg.(*Config).Validate()
}

func TestFactoryRejectsAnInvalidConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.MaxDevices = 0

	_, err := NewFactory().CreateMetrics(context.Background(), receivertest.NewNopSettings(NewFactory().Type()), cfg, consumertest.NewNop())

	require.Error(t, err)
}

func TestFactoryCreatesTheReceiverOnLinuxAndRefusesElsewhere(t *testing.T) {
	cfg := createDefaultConfig()

	r, err := NewFactory().CreateMetrics(context.Background(), receivertest.NewNopSettings(NewFactory().Type()), cfg, consumertest.NewNop())

	if runtime.GOOS == "linux" {
		require.NoError(t, err)
		assert.NotNil(t, r)
		return
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available on this platform")
}
