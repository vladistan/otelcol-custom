// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestFactoryTypeAndStability(t *testing.T) {
	f := NewFactory()

	assert.Equal(t, "ipmi", f.Type().String())
	assert.Equal(t, component.StabilityLevelDevelopment, f.MetricsStability())
}

func TestFactoryCreatesMetricsOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	_, logsErr := f.CreateLogs(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())
	_, tracesErr := f.CreateTraces(context.Background(), set, f.CreateDefaultConfig(), consumertest.NewNop())

	assert.Error(t, logsErr)
	assert.Error(t, tracesErr)
}

func TestFactoryRejectsAConfigWithoutBMCs(t *testing.T) {
	f := NewFactory()

	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "bmcs")
}

func TestFactoryCreatesTheReceiverFromAValidConfig(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.BMCs = validConfig().BMCs

	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, consumertest.NewNop())

	require.NoError(t, err)
	require.NoError(t, r.Shutdown(context.Background()))
}

// An authentication failure is counted per BMC in the receiver's own telemetry.
func TestAuthFailuresAreCountedInTheCollectorsOwnTelemetryPerBMC(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set, _ := settingsWithLogs()
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	cfg := fastConfig("a", "b")
	r := newReceiver(set, cfg, consumertest.NewNop(), func(_ context.Context, c BMCConfig, _ string) (bmcSession, error) {
		if c.Name == "a" {
			return nil, errAuth
		}
		return nil, errRefused
	})
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return authFailureCount(t, reader, "a") >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Equal(t, int64(1), authFailureCount(t, reader, "a"))
	assert.Equal(t, int64(0), authFailureCount(t, reader, "b"))
}

func authFailureCount(t *testing.T, reader sdkmetric.Reader, bmc string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "otelcol_hw_bmc_auth_failures" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, dp := range sum.DataPoints {
				if v, found := dp.Attributes.Value("bmc"); found && v.AsString() == bmc {
					return dp.Value
				}
			}
		}
	}
	return 0
}
