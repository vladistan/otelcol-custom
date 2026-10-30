// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestSelfMetricNames(t *testing.T) {
	want := map[string]string{
		metricSeriesActive:     "otelcol_processor_windowhist_series_active",
		metricSamplesReceived:  "otelcol_processor_windowhist_samples_received",
		metricSamplesDiscarded: "otelcol_processor_windowhist_samples_discarded",
	}
	for got, name := range want {
		if got != name {
			t.Errorf("metric name = %q, want %q", got, name)
		}
	}
}

func TestSelfMetricValues(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"cpu":          {Metrics: []string{"cpu.util"}},
		"network_rate": {KeepCounter: boolPtr(false), Metrics: []string{"net.io"}},
	})
	// 3 valid + 1 NaN gauge samples, 1 late gauge sample, counter: baseline, rate, reset.
	f.flush(t, 60*time.Second, t0.Add(60*time.Second)) // window now starts at t0+60s
	f.consume(t, gaugeBatch("h1",
		sample{"cpu.util", 1, t0.Add(61 * time.Second)},
		sample{"cpu.util", 2, t0.Add(62 * time.Second)},
		sample{"cpu.util", 3, t0.Add(63 * time.Second)},
		sample{"cpu.util", math.NaN(), t0.Add(64 * time.Second)},
		sample{"cpu.util", 9, t0.Add(-5 * time.Second)},
	))
	f.feedCounter(t, "net.io", 61, 100)
	f.feedCounter(t, "net.io", 71, 200)
	f.feedCounter(t, "net.io", 81, 10)

	if got := f.selfMetric(t, metricSamplesReceived); got != 8 {
		t.Errorf("samples_received = %d, want 8 (every grouped point, kept or not)", got)
	}
	if got := f.discards(t, reasonInvalid); got != 1 {
		t.Errorf("discarded{invalid} = %d, want 1", got)
	}
	if got := f.discards(t, reasonLate); got != 1 {
		t.Errorf("discarded{late} = %d, want 1", got)
	}
	if got := f.discards(t, reasonCounterReset); got != 1 {
		t.Errorf("discarded{counter_reset} = %d, want 1", got)
	}
	if got := f.selfMetric(t, metricSeriesActive); got != 2 {
		t.Errorf("series_active = %d, want 2 (one gauge series, one counter series)", got)
	}
}

func TestSpacingWarnsOncePerGroup(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"cpu":    {Metrics: []string{"cpu.util", "cpu.other"}},
		"memory": {Metrics: []string{"mem.util"}},
	})
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(10)}, sample{"mem.util", 1, at(10)}))
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(20)}, sample{"mem.util", 1, at(20)})) // 10s: on time
	if len(f.warnings) != 0 {
		t.Fatalf("on-time samples warned: %v", f.warnings)
	}

	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(50)})) // 30s gap against a 10s scrape
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(100)}, sample{"cpu.other", 1, at(10)}, sample{"cpu.other", 1, at(60)}))
	if len(f.warnings) != 1 {
		t.Fatalf("got %d warnings, want one for the cpu group: %v", len(f.warnings), f.warnings)
	}
	if !strings.Contains(f.warnings[0], "cpu") {
		t.Errorf("warning does not name the group: %q", f.warnings[0])
	}

	f.consume(t, gaugeBatch("h1", sample{"mem.util", 1, at(90)})) // memory has its own once
	if len(f.warnings) != 2 {
		t.Fatalf("got %d warnings, want a separate one for memory: %v", len(f.warnings), f.warnings)
	}
}

func TestSpacingWithinHalfDoesNotWarn(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(10)}))
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(24)})) // 14s: +40%
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(34)})) // 10s
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, at(40)})) // 6s: -40%
	if len(f.warnings) != 0 {
		t.Fatalf("jitter inside 50%% warned: %v", f.warnings)
	}
}
