// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

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
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), cfg, sink, &fakeSource{readings: sampleReadings()})

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 2 }, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverSendsNothingWhenThereAreNoSensors(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	src := &fakeSource{err: errNoSensors}
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, src)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return src.reads.Load() >= 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
}

func TestReceiverSurvivesAConsumerError(t *testing.T) {
	sink := &failingSink{}
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), sink, &fakeSource{readings: sampleReadings()})

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return sink.calls.Load() >= 1 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestReceiverShutdownWithoutStartIsClean(t *testing.T) {
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), consumertest.NewNop(), &fakeSource{})

	require.NoError(t, r.Shutdown(context.Background()))
}
