// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewFactoryTypeIsKthread(t *testing.T) {
	assert.Equal(t, "kthread", NewFactory().Type().String())
}

func TestNewFactoryDefaultConfigHasTheThreeKernelThreadPatterns(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	require.True(t, ok)

	assert.Equal(t, []string{"kworker*", "ksoftirqd*", "migration*"}, cfg.Patterns)
	assert.Equal(t, "kernel_threads", cfg.AggregateName)
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

func TestCreateMetricsWarnsOnACatchAllPattern(t *testing.T) {
	f := NewFactory()
	core, logs := observer.New(zapcore.WarnLevel)
	set := processortest.NewNopSettings(f.Type())
	set.Logger = zap.New(core)

	_, err := f.CreateMetrics(context.Background(), set, &Config{Patterns: []string{"*"}, AggregateName: "kernel_threads"}, consumertest.NewNop())

	require.NoError(t, err, "a catch-all is allowed, only reported")
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].Message, "matches every process")
}

func TestCreateMetricsDefaultConfigDoesNotWarn(t *testing.T) {
	f := NewFactory()
	core, logs := observer.New(zapcore.WarnLevel)
	set := processortest.NewNopSettings(f.Type())
	set.Logger = zap.New(core)

	_, err := f.CreateMetrics(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())

	require.NoError(t, err)
	assert.Zero(t, logs.Len())
}

func TestCreateMetricsRejectsAnInvalidConfig(t *testing.T) {
	f := NewFactory()
	set := processortest.NewNopSettings(f.Type())

	_, err := f.CreateMetrics(context.Background(), set, &Config{AggregateName: "kernel_threads"}, consumertest.NewNop())

	assert.Error(t, err)
}
