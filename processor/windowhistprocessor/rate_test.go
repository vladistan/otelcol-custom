// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"math"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

func rateGroup(keepCounter bool) map[string]GroupConfig {
	return map[string]GroupConfig{
		"network_rate": {KeepCounter: boolPtr(keepCounter), Metrics: []string{"net.io"}},
	}
}

func (f *fixture) metricNamed(t *testing.T, name string) pmetric.Metric {
	t.Helper()
	for _, m := range f.allMetrics() {
		if m.Name() == name {
			return m
		}
	}
	t.Fatalf("no metric named %q in the sink", name)
	return pmetric.Metric{}
}

func (f *fixture) feedCounter(t *testing.T, name string, offsetSec int, value int64) {
	t.Helper()
	f.consume(t, counterBatch(name, value, t0.Add(time.Duration(offsetSec)*time.Second)))
}

func TestCounterRateUsesMeasuredTime(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 0)
	f.feedCounter(t, "net.io", 20, 1000) // 1000 over 10s = 100/s
	f.feedCounter(t, "net.io", 40, 1600) // 600 over 20s = 30/s
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	dp := f.metricNamed(t, "net.io.rate").ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 2 || math.Abs(dp.Sum()-130) > 1e-9 {
		t.Errorf("count/sum = %d/%v, want 2/130", dp.Count(), dp.Sum())
	}
	if dp.Min() != 30 || dp.Max() != 100 {
		t.Errorf("min/max = %v/%v, want 30/100", dp.Min(), dp.Max())
	}
}

func TestRateMetricNameAndUnit(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 0)
	f.feedCounter(t, "net.io", 20, 100)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	m := f.metricNamed(t, "net.io.rate")
	if m.Unit() != "By/s" {
		t.Errorf("unit = %q, want By/s", m.Unit())
	}
	eh := m.ExponentialHistogram()
	if eh.AggregationTemporality() != pmetric.AggregationTemporalityDelta {
		t.Errorf("temporality = %v, want delta", eh.AggregationTemporality())
	}
}

func TestCounterDropIsSkippedAndRebaselined(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 100)
	f.feedCounter(t, "net.io", 20, 200) // 10/s
	f.feedCounter(t, "net.io", 30, 50)  // drop: reset, new baseline 50
	f.feedCounter(t, "net.io", 40, 150) // 100 over 10s = 10/s
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	dp := f.metricNamed(t, "net.io.rate").ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 2 || dp.Sum() != 20 {
		t.Errorf("count/sum = %d/%v, want 2/20 (the reset pair is not a sample)", dp.Count(), dp.Sum())
	}
	if got := f.discards(t, reasonCounterReset); got != 1 {
		t.Errorf("counter_reset discards = %d, want 1", got)
	}
}

func TestStartTimeChangeIsReset(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 100)
	f.consume(t, counterBatchStart("net.io", 300, t0.Add(20*time.Second), t0.Add(15*time.Second)))
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	if n := f.sink.DataPointCount(); n != 0 {
		t.Errorf("a restarted counter produced %d points, want none", n)
	}
	if got := f.discards(t, reasonCounterReset); got != 1 {
		t.Errorf("counter_reset discards = %d, want 1", got)
	}
}

func TestNonPositiveTimeGapIsInvalid(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 100)
	f.feedCounter(t, "net.io", 10, 200)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	if n := f.sink.DataPointCount(); n != 0 {
		t.Errorf("a zero time gap produced %d points, want none", n)
	}
	if got := f.discards(t, reasonInvalid); got != 1 {
		t.Errorf("invalid discards = %d, want 1", got)
	}
}

func TestKeepCounterEmitsLastCounterPoint(t *testing.T) {
	f := newFixture(t, rateGroup(true))
	f.feedCounter(t, "net.io", 10, 0)
	f.feedCounter(t, "net.io", 20, 1000)
	f.feedCounter(t, "net.io", 30, 2500)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	counter := f.metricNamed(t, "net.io")
	if counter.Type() != pmetric.MetricTypeSum || !counter.Sum().IsMonotonic() ||
		counter.Sum().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
		t.Fatalf("the kept counter changed shape: %v", counter.Type())
	}
	if counter.Unit() != "By" {
		t.Errorf("counter unit = %q, want By", counter.Unit())
	}
	if got := counter.Sum().DataPoints().At(0).IntValue(); got != 2500 {
		t.Errorf("counter value = %d, want the last 2500", got)
	}
	f.metricNamed(t, "net.io.rate")
}

func TestKeepCounterOffEmitsOnlyRate(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 10, 0)
	f.feedCounter(t, "net.io", 20, 1000)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	for _, m := range f.allMetrics() {
		if m.Name() == "net.io" {
			t.Fatal("keep_counter=false still emitted the counter")
		}
	}
}

func TestKeepCounterWithOnlyBaselineInWindow(t *testing.T) {
	f := newFixture(t, rateGroup(true))
	f.feedCounter(t, "net.io", 10, 700)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	ms := f.allMetrics()
	if len(ms) != 1 || ms[0].Name() != "net.io" {
		t.Fatalf("got %d metrics, want only the counter (no rate exists yet)", len(ms))
	}
}

func TestRateBaselineSurvivesWindowBoundary(t *testing.T) {
	f := newFixture(t, rateGroup(false))
	f.feedCounter(t, "net.io", 50, 100)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	f.feedCounter(t, "net.io", 70, 300) // 200 over 20s = 10/s across the boundary
	f.flush(t, 60*time.Second, t0.Add(120*time.Second))

	dp := f.metricNamed(t, "net.io.rate").ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 1 || dp.Sum() != 10 {
		t.Errorf("count/sum = %d/%v, want 1/10", dp.Count(), dp.Sum())
	}
}
