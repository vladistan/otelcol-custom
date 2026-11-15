// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func newTestReceiver(t *testing.T, cfg *Config, sink *consumertest.MetricsSink) *zfsReceiver {
	t.Helper()
	f := NewFactory()
	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, sink)
	require.NoError(t, err)
	return r.(*zfsReceiver)
}

func TestReceiverSendsFixtureMetricsOnStartAndStopsCleanly(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, hostConfig(), sink)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 1 }, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Contains(t, metricNames(sink.AllMetrics()[0]), "zfs.pool.io.bytes")
}

func TestReceiverScrapesAgainEachInterval(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := hostConfig()
	cfg.CollectionInterval = time.Second
	r := newTestReceiver(t, cfg, sink)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { _ = r.Shutdown(context.Background()) }()

	require.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 4*time.Second, 50*time.Millisecond)
}

func TestReceiverSendsNothingWithoutZfs(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := hostConfig()
	cfg.KstatPath = t.TempDir() + "/none"
	r := newTestReceiver(t, cfg, sink)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
}

func TestReceiverSurvivesAConsumerError(t *testing.T) {
	r := newTestReceiver(t, hostConfig(), nil)
	r.consumer = consumertest.NewErr(errors.New("downstream refused"))

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(100 * time.Millisecond)

	assert.NoError(t, r.Shutdown(context.Background()))
}

func TestShutdownBeforeStartIsSafe(t *testing.T) {
	r := newTestReceiver(t, hostConfig(), new(consumertest.MetricsSink))

	assert.NoError(t, r.Shutdown(context.Background()))
}
