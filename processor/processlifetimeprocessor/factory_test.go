// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package processlifetimeprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestNewFactoryTypeIsProcesslifetime(t *testing.T) {
	assert.Equal(t, "processlifetime", NewFactory().Type().String())
}

func TestNewFactoryDefaultConfigHasNoKeys(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	require.True(t, ok)

	assert.NoError(t, cfg.Validate())
}

func TestNewFactoryOnlyMetricsAreSupported(t *testing.T) {
	f := NewFactory()
	set := processortest.NewNopSettings(f.Type())

	_, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.NoError(t, err)
	_, err = f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.True(t, errors.Is(err, pipeline.ErrSignalNotSupported))
	_, err = f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	assert.True(t, errors.Is(err, pipeline.ErrSignalNotSupported))
}
