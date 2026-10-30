// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"testing"
	"time"
)

func TestSeriesIdleForTwoWindowsIsEvicted(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 1)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second)) // idle window 1
	if got := f.selfMetric(t, metricSeriesActive); got != 1 {
		t.Fatalf("series_active after one idle window = %d, want 1", got)
	}
	f.flush(t, 60*time.Second, t0.Add(180*time.Second)) // idle window 2
	if got := f.selfMetric(t, metricSeriesActive); got != 0 {
		t.Fatalf("series_active after two idle windows = %d, want 0", got)
	}
}

func TestSampleResetsIdleCount(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 1)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second)) // idle 1
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 2, t0.Add(130 * time.Second)}))
	f.flush(t, 60*time.Second, t0.Add(180*time.Second)) // sample: idle back to 0
	f.flush(t, 60*time.Second, t0.Add(240*time.Second)) // idle 1
	if got := f.selfMetric(t, metricSeriesActive); got != 1 {
		t.Fatalf("series_active = %d, want 1 (one idle window since the last sample)", got)
	}
}

func TestEvictionDropsCounterBaseline(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 100)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second))
	f.flush(t, 60*time.Second, t0.Add(180*time.Second)) // evicted

	f.feedCounter(t, "net.io", 190, 5000) // a new baseline, not a 4900-count jump
	f.flush(t, 60*time.Second, t0.Add(240*time.Second))
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("an evicted series' baseline survived: %d points emitted", n)
	}
}

func TestShutdownFlushesPartialWindow(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 1, 2)
	end := t0.Add(25 * time.Second)
	f.proc.now = func() time.Time { return end }

	if err := f.proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	ms := f.allMetrics()
	if len(ms) != 1 {
		t.Fatalf("got %d metrics, want one partial window", len(ms))
	}
	dp := ms[0].ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 2 {
		t.Errorf("count = %d, want 2", dp.Count())
	}
	if !dp.StartTimestamp().AsTime().Equal(t0) || !dp.Timestamp().AsTime().Equal(end) {
		t.Errorf("window = %v..%v, want the real %v..%v",
			dp.StartTimestamp().AsTime(), dp.Timestamp().AsTime(), t0, end)
	}
}

func TestShutdownFlushesEachSendInterval(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"cpu":    {Metrics: []string{"cpu.util"}},
		"memory": {SendInterval: 120 * time.Second, Metrics: []string{"mem.util"}},
	})
	f.consume(t, gaugeBatch("h1",
		sample{"cpu.util", 1, t0.Add(10 * time.Second)},
		sample{"mem.util", 2, t0.Add(10 * time.Second)},
	))
	if err := f.proc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.allMetrics()); got != 2 {
		t.Fatalf("got %d metrics, want one per send interval", got)
	}
}

func TestShutdownWithNothingBufferedEmitsNothing(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	if err := f.proc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("empty shutdown emitted %d points", n)
	}
}
