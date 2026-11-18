// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type fakeSource struct {
	inv   inventory
	calls atomic.Int32
	block chan struct{}

	active, maxActive atomic.Int32
}

func (f *fakeSource) read(context.Context) inventory {
	f.calls.Add(1)
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for m := f.maxActive.Load(); n > m && !f.maxActive.CompareAndSwap(m, n); m = f.maxActive.Load() {
	}
	if f.block != nil {
		<-f.block
	}
	return f.inv
}

func physicalInventory() inventory {
	return inventory{
		host: hostPhysical,
		facts: []fact{
			{metric: metricCPU, attrs: map[string]any{"hw.id": "cpu/0", "hw.model": "Test CPU", "cpu.cores": int64(4)}},
			{metric: metricDrive, attrs: map[string]any{"hw.id": "SER1", "hw.serial_number": "SER1", "drive.size": int64(1000)}},
		},
		status: map[string]bool{"cpu": true, "memory": false, "drive": true},
	}
}

type rig struct {
	r      *inventoryReceiver
	sink   *consumertest.MetricsSink
	logs   *observer.ObservedLogs
	ticks  chan time.Time
	period time.Duration
	src    *fakeSource
}

func newRig(t *testing.T, src *fakeSource) *rig {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	settings := receivertest.NewNopSettings(component.MustNewType("inventory"))
	settings.Logger = zap.New(core)
	g := &rig{sink: new(consumertest.MetricsSink), logs: logs, ticks: make(chan time.Time), src: src}
	cfg := createDefaultConfig().(*Config)
	g.r = newReceiver(settings, cfg, g.sink, src)
	g.r.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		g.period = d
		return g.ticks, func() {}
	}
	return g
}

func (g *rig) start(t *testing.T) {
	t.Helper()
	require.NoError(t, g.r.Start(t.Context(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = g.r.Shutdown(context.Background()) })
}

func (g *rig) waitBatches(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return len(g.sink.AllMetrics()) >= n }, 5*time.Second, 5*time.Millisecond)
}

func metricNames(md pmetric.Metrics) []string {
	var out []string
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		ms := rms.At(i).ScopeMetrics().At(0).Metrics()
		for j := 0; j < ms.Len(); j++ {
			out = append(out, ms.At(j).Name())
		}
	}
	return out
}

func TestReceiverCollectsAtStartThenOnlyAtTheInterval(t *testing.T) {
	g := newRig(t, &fakeSource{inv: physicalInventory()})

	g.start(t)
	g.waitBatches(t, 1)

	assert.Equal(t, 24*time.Hour, g.period)
	assert.Len(t, g.sink.AllMetrics(), 1, "nothing before the first tick")

	// The tick channel is unbuffered: the send returns once the loop is waiting again.
	g.ticks <- time.Now()
	g.waitBatches(t, 2)
	assert.Equal(t, int32(2), g.src.calls.Load(), "one read per tick")
}

func TestReceiverEmitsInfoGaugesOfValueOneAndStatusPerSource(t *testing.T) {
	g := newRig(t, &fakeSource{inv: physicalInventory()})

	g.start(t)
	g.waitBatches(t, 1)

	md := g.sink.AllMetrics()[0]
	assert.ElementsMatch(t, []string{metricCPU, metricDrive, metricStatus}, metricNames(md))
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	status := map[string]int64{}
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i)
		require.Equal(t, pmetric.MetricTypeGauge, m.Type(), m.Name())
		assert.Equal(t, "1", m.Unit())
		dps := m.Gauge().DataPoints()
		for j := 0; j < dps.Len(); j++ {
			dp := dps.At(j)
			if m.Name() == metricStatus {
				src, _ := dp.Attributes().Get(attrSource)
				status[src.Str()] = dp.IntValue()
				continue
			}
			assert.Equal(t, int64(1), dp.IntValue(), m.Name())
		}
	}
	assert.Equal(t, map[string]int64{srcCPU: 1, srcMemory: 0, srcDrive: 1}, status)
	cpu := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes()
	cores, _ := cpu.Get("cpu.cores")
	assert.Equal(t, int64(4), cores.Int())
}

func TestReceiverEmitsNoUptimeOrBootSeries(t *testing.T) {
	g := newRig(t, &fakeSource{inv: physicalInventory()})

	g.start(t)
	g.waitBatches(t, 1)

	for _, name := range metricNames(g.sink.AllMetrics()[0]) {
		assert.False(t, strings.Contains(name, "uptime") || strings.Contains(name, "boot"), name)
		assert.True(t, strings.HasPrefix(name, "inventory."), name)
	}
}

func TestReceiverSendsNothingFromAVirtualHostAndLogsOnePerStart(t *testing.T) {
	inv := inventory{host: hostVirtual, hostReason: "hypervisor flag"}
	g := newRig(t, &fakeSource{inv: inv})

	g.start(t)
	require.Eventually(t, func() bool { return g.src.calls.Load() >= 1 }, 5*time.Second, time.Millisecond)
	g.ticks <- time.Now()
	require.Eventually(t, func() bool { return g.src.calls.Load() >= 2 }, 5*time.Second, time.Millisecond)

	assert.Empty(t, g.sink.AllMetrics())
	assert.Equal(t, 1, g.logs.FilterMessageSnippet("virtual").Len())
	assert.Zero(t, g.logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestReceiverTreatsAnUndecidedHostAsPhysicalWithOneInfoLine(t *testing.T) {
	inv := physicalInventory()
	inv.host = hostUndecided
	g := newRig(t, &fakeSource{inv: inv})

	g.start(t)
	g.waitBatches(t, 1)
	g.ticks <- time.Now()
	g.waitBatches(t, 2)

	assert.Equal(t, 1, g.logs.FilterMessageSnippet("cannot be decided").Len())
}

func TestReceiverLogsUnreadableDMIOncePerStartAcrossTwoCollections(t *testing.T) {
	inv := physicalInventory()
	inv.dmiUnreadable = true
	g := newRig(t, &fakeSource{inv: inv})

	g.start(t)
	g.waitBatches(t, 1)
	g.ticks <- time.Now()
	g.waitBatches(t, 2)

	assert.Equal(t, 1, g.logs.FilterMessageSnippet("DMI").Len())
	assert.Zero(t, g.logs.FilterLevelExact(zapcore.WarnLevel).Len(), "a missing fact is never a warning")
}

func TestReceiverLogsAFactThatWasLeftOutAtDebugLevelOnly(t *testing.T) {
	inv := physicalInventory()
	inv.notes = []string{"drive size is not readable"}
	g := newRig(t, &fakeSource{inv: inv})

	g.start(t)
	g.waitBatches(t, 1)

	left := g.logs.FilterMessageSnippet("left out")
	require.Equal(t, 1, left.Len())
	entry := left.All()[0]
	assert.Equal(t, zapcore.DebugLevel, entry.Level)
	assert.Equal(t, "drive size is not readable", entry.ContextMap()["fact"])
	assert.Zero(t, g.logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestReceiverShutdownDoesNotWaitForABlockedRead(t *testing.T) {
	src := &fakeSource{inv: physicalInventory(), block: make(chan struct{})}
	g := newRig(t, src)
	g.start(t)
	require.Eventually(t, func() bool { return src.calls.Load() == 1 }, 5*time.Second, time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- g.r.Shutdown(context.Background()) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown waited for the blocked read")
	}
	close(src.block)
}

func TestReceiverDoesNotStartASecondReadWhileOneIsRunning(t *testing.T) {
	src := &fakeSource{inv: physicalInventory(), block: make(chan struct{})}
	g := newRig(t, src)
	g.ticks = make(chan time.Time, 1) // the loop is busy in the read, so the tick has to queue
	g.start(t)
	require.Eventually(t, func() bool { return src.calls.Load() == 1 }, 5*time.Second, time.Millisecond)

	g.ticks <- time.Now()
	close(src.block)
	g.waitBatches(t, 2)

	assert.Equal(t, int32(1), src.maxActive.Load(), "reads never overlap")
}

type failingSink struct {
	consumertest.MetricsSink
	fail atomic.Bool
}

func (f *failingSink) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	if f.fail.Load() {
		return errors.New("downstream refused")
	}
	return f.MetricsSink.ConsumeMetrics(ctx, md)
}

func TestReceiverLogsASendFailureOncePerEpisode(t *testing.T) {
	g := newRig(t, &fakeSource{inv: physicalInventory()})
	sink := &failingSink{}
	sink.fail.Store(true)
	g.r.consumer = sink

	g.start(t)
	require.Eventually(t, func() bool { return g.src.calls.Load() >= 1 }, 5*time.Second, time.Millisecond)
	g.ticks <- time.Now()
	// This send returns only after the second collection has finished and logged.
	g.ticks <- time.Now()

	assert.Equal(t, 1, g.logs.FilterLevelExact(zapcore.ErrorLevel).Len())
}

func TestReceiverLogsNeverCarryASerialOrHostName(t *testing.T) {
	g := newRig(t, &fakeSource{inv: physicalInventory()})

	g.start(t)
	g.waitBatches(t, 1)

	for _, e := range g.logs.All() {
		assert.NotContains(t, e.Message, "SER1")
		for _, f := range e.Context {
			assert.NotContains(t, f.String, "SER1")
		}
	}
}
