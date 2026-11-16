// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

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

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func newTestReceiver(t *testing.T, bus *fakeBus, sink *consumertest.MetricsSink, delay time.Duration, mutate ...func(*Config)) *smartReceiver {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = time.Second
	cfg.CommandTimeout = time.Second
	for _, m := range mutate {
		m(cfg)
	}
	require.NoError(t, cfg.Validate())
	counter, err := newSkippedCounter(component.TelemetrySettings{MeterProvider: sdkmetric.NewMeterProvider()})
	require.NoError(t, err)
	r, err := newReceiver(receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink, bus, counter, delay)
	require.NoError(t, err)
	return r
}

func TestStartSendsNoCommandToAnyDisk(t *testing.T) {
	ata, nvme := smarttest.HealthyATA("SN1"), smarttest.HealthyNVMe("SN9")
	bus := newBus()
	bus.addATA("sda", ata)
	bus.addNVMe("nvme0", nvme)
	r := newTestReceiver(t, bus, new(consumertest.MetricsSink), time.Hour)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { require.NoError(t, r.Shutdown(context.Background())) }()

	assert.Empty(t, ata.Sent)
	assert.Empty(t, nvme.Sent)
	assert.Empty(t, bus.opened)
}

func TestTheFirstReadComesAfterTheStartDelayAndDataArrives(t *testing.T) {
	bus := newBus()
	bus.addNVMe("nvme0", smarttest.HealthyNVMe("SN9"))
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, bus, sink, 50*time.Millisecond)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	defer func() { require.NoError(t, r.Shutdown(context.Background())) }()

	require.Eventually(t, func() bool { return sink.DataPointCount() > 0 }, 5*time.Second, 20*time.Millisecond)
}

func TestShutdownBeforeTheFirstReadReadsNothing(t *testing.T) {
	bus := newBus()
	nvme := smarttest.HealthyNVMe("SN9")
	bus.addNVMe("nvme0", nvme)
	r := newTestReceiver(t, bus, new(consumertest.MetricsSink), time.Hour)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))

	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, nvme.Sent)
}

// hungNVMe blocks in Admin until released, as a controller that never answers does.
type hungNVMe struct {
	entered chan struct{}
	release chan struct{}
}

func (h *hungNVMe) Admin(smartdecode.NVMeCommand) ([]byte, error) {
	select {
	case h.entered <- struct{}{}:
	default:
	}
	<-h.release
	return nil, smartdecode.ErrTimeout
}

func TestShutdownReturnsWhenItsContextEndsDuringAHungRead(t *testing.T) {
	hung := &hungNVMe{entered: make(chan struct{}, 1), release: make(chan struct{})}
	bus := newBus()
	bus.addNVMe("nvme0", hung)
	r := newTestReceiver(t, bus, new(consumertest.MetricsSink), time.Millisecond)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	<-hung.entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := r.Shutdown(ctx)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(hung.release)
}

func TestAnEmptyScrapeSendsNothing(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	r := newTestReceiver(t, newBus(), sink, 10*time.Millisecond)

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Zero(t, sink.DataPointCount())
}

func TestShutdownWithoutStartIsSafe(t *testing.T) {
	r := newTestReceiver(t, newBus(), new(consumertest.MetricsSink), time.Hour)

	require.NoError(t, r.Shutdown(context.Background()))
}
