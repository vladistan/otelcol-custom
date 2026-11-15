// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

type failingSink struct{ calls atomic.Int32 }

func (*failingSink) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (f *failingSink) ConsumeMetrics(context.Context, pmetric.Metrics) error {
	f.calls.Add(1)
	return errors.New("downstream down")
}

func TestReceiverEmitsAtStartAndOnEachInterval(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	cfg := testConfig()
	cfg.CollectionInterval = time.Second
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), cfg, sink, enaNIC())

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverSendsNothingWhenNoSeries(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	nic := enaNIC()
	nic.drivers["eth0"] = "virtio_net"
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, nic)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
}

func TestReceiverSurvivesAConsumerError(t *testing.T) {
	sink := &failingSink{}
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, enaNIC())

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return sink.calls.Load() >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverShutdownWithoutStartIsClean(t *testing.T) {
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), consumertest.NewNop(), enaNIC())

	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverStartsTheCountersAtBootTime(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	nic := enaNIC()
	nic.boot = time.Unix(500, 0)
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, nic)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	dp := sink.AllMetrics()[0].ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	assert.Equal(t, pcommon.NewTimestampFromTime(time.Unix(500, 0)), dp.StartTimestamp())
}

func TestReceiverFallsBackToTheStartTimeWhenBootTimeIsUnknown(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	nic := enaNIC()
	nic.bootErr = errors.New("no boot time")
	before := time.Now()
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, nic)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	dp := sink.AllMetrics()[0].ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	assert.GreaterOrEqual(t, dp.StartTimestamp(), pcommon.NewTimestampFromTime(before))
}
