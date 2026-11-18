// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func newTestReceiver(t *testing.T, b *fakeBus, sink *consumertest.MetricsSink, firstDelay time.Duration) *diskReceiver {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = time.Second
	h := newHarness(t, b)
	r, err := newReceiver(receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink, b, h.scraper.skipped, firstDelay)
	require.NoError(t, err)
	return r
}

func TestReceiver_StartSendsNoCommandToAnyDisk(t *testing.T) {
	b := newFakeBus()
	d := b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	r := newTestReceiver(t, b, new(consumertest.MetricsSink), time.Hour)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Zero(t, b.discovers, "the disks are not even listed before the first read")
	assert.Empty(t, b.opened)
	assert.Zero(t, d.sent)
}

func TestReceiver_ReadsAfterTheFirstDelayAndSendsTheMetrics(t *testing.T) {
	b := newFakeBus()
	b.add("PhysicalDrive0", sataDisk(smarttest.HealthyATA("SN1")))
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, b, sink, time.Millisecond)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return len(sink.AllMetrics()) > 0 }, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
	assert.Positive(t, sink.AllMetrics()[0].MetricCount())
}

func TestReceiver_ShutdownBeforeStartIsFine(t *testing.T) {
	r := newTestReceiver(t, newFakeBus(), new(consumertest.MetricsSink), time.Hour)
	assert.NoError(t, r.Shutdown(context.Background()))
}

func TestFactory_CreateOnAnotherSystemReportsWindowsOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows build opens real disks")
	}
	f := NewFactory()
	assert.Equal(t, typeStr, f.Type().String())
	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), new(consumertest.MetricsSink))
	assert.ErrorIs(t, err, errSystemOnly)
}

func TestFactory_CreateRejectsABadConfig(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.MaxDevices = 0
	_, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, new(consumertest.MetricsSink))
	assert.ErrorContains(t, err, "max_devices")
}
