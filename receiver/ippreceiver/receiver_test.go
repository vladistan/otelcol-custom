// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const wait = 5 * time.Second

func startReceiver(t *testing.T, cfg *Config, logger *zap.Logger) *consumertest.MetricsSink {
	t.Helper()
	sink := new(consumertest.MetricsSink)
	set := receivertest.NewNopSettings(NewFactory().Type())
	if logger != nil {
		set.Logger = logger
	}
	r, err := NewFactory().CreateMetrics(context.Background(), set, cfg, sink)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { require.NoError(t, r.Shutdown(context.Background())) })
	return sink
}

func fastConfig(printers ...Printer) *Config {
	return &Config{CollectionInterval: 40 * time.Millisecond, Timeout: time.Second, Printers: printers}
}

// pointsFor returns the points of one printer seen so far.
func pointsFor(sink *consumertest.MetricsSink, printer, metric string) []point {
	var out []point
	for _, md := range sink.AllMetrics() {
		for _, p := range named(pointsOf(md), metric) {
			if p.attrs["hw.name"] == printer {
				out = append(out, p)
			}
		}
	}
	return out
}

func TestPollsEveryPrinterEachIntervalWithOnlyGetPrinterAttributes(t *testing.T) {
	a := newFakePrinter(t, healthyAttrs()...)
	b := newFakePrinter(t, healthyAttrs()...)
	sink := startReceiver(t, fastConfig(
		Printer{Name: "lab-a", Endpoint: a.endpoint()},
		Printer{Name: "lab-b", Endpoint: b.endpoint()},
	), nil)

	require.Eventually(t, func() bool {
		return len(pointsFor(sink, "lab-a", "printer.ipp.reachable")) >= 3 &&
			len(pointsFor(sink, "lab-b", "printer.ipp.reachable")) >= 3
	}, wait, 10*time.Millisecond)

	for _, f := range []*fakePrinter{a, b} {
		ops, requested := f.recorded()
		require.NotEmpty(t, ops)
		for _, op := range ops {
			assert.Equal(t, uint16(0x000B), op)
		}
		assert.Len(t, requested[0], 7)
	}
}

func TestUnreachableThenReachableAgain(t *testing.T) {
	p := newFakePrinter(t, healthyAttrs()...)
	sink := startReceiver(t, fastConfig(Printer{Name: "lab-a", Endpoint: p.endpoint()}), nil)
	require.Eventually(t, func() bool {
		return len(pointsFor(sink, "lab-a", "printer.ipp.reachable")) >= 1
	}, wait, 10*time.Millisecond)

	p.set(0x0400, 0) // client-error-bad-request
	failed := countReachable(sink, 0)
	require.Eventually(t, func() bool { return countReachable(sink, 0) > failed }, wait, 10*time.Millisecond)

	p.set(0, 0, healthyAttrs()...)
	require.Eventually(t, func() bool { return lastReachable(sink) == 1 }, wait, 10*time.Millisecond)
}

func countReachable(sink *consumertest.MetricsSink, value float64) int {
	n := 0
	for _, p := range pointsFor(sink, "lab-a", "printer.ipp.reachable") {
		if p.value == value {
			n++
		}
	}
	return n
}

func lastReachable(sink *consumertest.MetricsSink) float64 {
	pts := pointsFor(sink, "lab-a", "printer.ipp.reachable")
	if len(pts) == 0 {
		return -1
	}
	return pts[len(pts)-1].value
}

func TestAFailedPollEmitsOnlyReachableZero(t *testing.T) {
	p := newFakePrinter(t, healthyAttrs()...)
	p.set(0x0500, 0, healthyAttrs()...) // server-error-internal-error with attributes
	sink := startReceiver(t, fastConfig(Printer{Name: "lab-a", Endpoint: p.endpoint()}), nil)

	require.Eventually(t, func() bool { return countReachable(sink, 0) >= 1 }, wait, 10*time.Millisecond)

	for _, md := range sink.AllMetrics() {
		pts := pointsOf(md)
		require.Len(t, pts, 1)
		assert.Equal(t, "printer.ipp.reachable", pts[0].metric)
	}
}

func TestClosedPortGivesReachableZero(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	sink := startReceiver(t, fastConfig(Printer{Name: "lab-a", Endpoint: "ipp://" + addr + "/ipp/print"}), nil)

	require.Eventually(t, func() bool { return countReachable(sink, 0) >= 1 }, wait, 10*time.Millisecond)
	assert.Zero(t, countReachable(sink, 1))
}

func TestAnswerSlowerThanTheTimeoutGivesReachableZero(t *testing.T) {
	p := newFakePrinter(t, healthyAttrs()...)
	p.set(0, 2*time.Second, healthyAttrs()...)
	cfg := fastConfig(Printer{Name: "lab-a", Endpoint: p.endpoint()})
	cfg.Timeout = 100 * time.Millisecond
	sink := startReceiver(t, cfg, nil)

	require.Eventually(t, func() bool { return countReachable(sink, 0) >= 1 }, wait, 10*time.Millisecond)

	assert.Zero(t, countReachable(sink, 1))
}

func TestABlockedPrinterDoesNotDelayAnother(t *testing.T) {
	slow := newFakePrinter(t, healthyAttrs()...).blocked()
	fast := newFakePrinter(t, healthyAttrs()...)
	cfg := fastConfig(
		Printer{Name: "slow", Endpoint: slow.endpoint()},
		Printer{Name: "fast", Endpoint: fast.endpoint()},
	)
	cfg.Timeout = 3 * time.Second
	sink := startReceiver(t, cfg, nil)

	require.Eventually(t, func() bool {
		return len(pointsFor(sink, "fast", "printer.ipp.reachable")) >= 4
	}, time.Second, 10*time.Millisecond)

	assert.Empty(t, pointsFor(sink, "slow", "printer.ipp.reachable"))
}

func TestOneWarningPerFailureEpisode(t *testing.T) {
	p := newFakePrinter(t, healthyAttrs()...)
	p.set(0x0400, 0)
	core, logs := observer.New(zap.WarnLevel)
	sink := startReceiver(t, fastConfig(Printer{Name: "lab-a", Endpoint: p.endpoint()}), zap.New(core))

	require.Eventually(t, func() bool { return countReachable(sink, 0) >= 4 }, wait, 10*time.Millisecond)
	assert.Equal(t, 1, logs.FilterMessage("Printer poll failed").Len())

	p.set(0, 0, healthyAttrs()...)
	require.Eventually(t, func() bool { return lastReachable(sink) == 1 }, wait, 10*time.Millisecond)
	p.set(0x0400, 0)
	before := countReachable(sink, 0)
	require.Eventually(t, func() bool { return countReachable(sink, 0) >= before+2 }, wait, 10*time.Millisecond)

	assert.Equal(t, 2, logs.FilterMessage("Printer poll failed").Len())
}

func TestAnswerWithoutPrinterAttributesGivesReachableZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte{1, 1, 0, 0, 0, 0, 0, 1, 0x01, 0x03})
	}))
	t.Cleanup(srv.Close)
	endpoint := "ipp://" + strings.TrimPrefix(srv.URL, "http://") + "/ipp/print"
	sink := startReceiver(t, fastConfig(Printer{Name: "lab-a", Endpoint: endpoint}), nil)

	require.Eventually(t, func() bool { return countReachable(sink, 0) >= 1 }, wait, 10*time.Millisecond)

	assert.Zero(t, countReachable(sink, 1))
}

func TestShutdownStopsPolling(t *testing.T) {
	p := newFakePrinter(t, healthyAttrs()...)
	sink := new(consumertest.MetricsSink)
	r, err := NewFactory().CreateMetrics(context.Background(), receivertest.NewNopSettings(NewFactory().Type()),
		fastConfig(Printer{Name: "lab-a", Endpoint: p.endpoint()}), sink)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return sink.DataPointCount() > 0 }, wait, 10*time.Millisecond)

	require.NoError(t, r.Shutdown(context.Background()))
	after := len(sink.AllMetrics())
	time.Sleep(150 * time.Millisecond)

	assert.Equal(t, after, len(sink.AllMetrics()))
}
