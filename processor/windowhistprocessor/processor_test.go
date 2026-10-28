// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"math"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var t0 = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

type fixture struct {
	proc *windowProcessor
	sink *consumertest.MetricsSink
}

func newFixture(t *testing.T, groups map[string]GroupConfig) *fixture {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	cfg.Groups = groups
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	sink := new(consumertest.MetricsSink)
	proc, err := newWindowProcessor(cfg, sink)
	if err != nil {
		t.Fatalf("newWindowProcessor: %v", err)
	}
	proc.now = func() time.Time { return t0 }
	proc.initWindows(t0)
	return &fixture{proc: proc, sink: sink}
}

type sample struct {
	name  string
	value float64
	at    time.Time
}

func gaugeBatch(host string, samples ...sample) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("host.name", host)
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("test")
	for _, s := range samples {
		m := sm.Metrics().AppendEmpty()
		m.SetName(s.name)
		m.SetUnit("1")
		dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(s.value)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(s.at))
	}
	return md
}

func counterBatch(name string, value int64, at time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := sum.DataPoints().AppendEmpty()
	dp.SetIntValue(value)
	dp.SetStartTimestamp(pcommon.NewTimestampFromTime(t0))
	dp.SetTimestamp(pcommon.NewTimestampFromTime(at))
	return md
}

func (f *fixture) consume(t *testing.T, md pmetric.Metrics) {
	t.Helper()
	if err := f.proc.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("ConsumeMetrics: %v", err)
	}
}

func (f *fixture) flush(t *testing.T, send time.Duration, end time.Time) {
	t.Helper()
	if err := f.proc.flush(context.Background(), send, end); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func (f *fixture) allMetrics() []pmetric.Metric {
	var out []pmetric.Metric
	for _, md := range f.sink.AllMetrics() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			sms := rms.At(i).ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					out = append(out, ms.At(k))
				}
			}
		}
	}
	return out
}

func feedWindow(t *testing.T, f *fixture, name string, values ...float64) {
	t.Helper()
	for i, v := range values {
		at := t0.Add(time.Duration(i+1) * 10 * time.Second)
		f.consume(t, gaugeBatch("h1", sample{name, v, at}))
	}
}

func TestHistogramGroupEmitsOneDeltaPointPerWindow(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 0.1, 0.2, 0.3, 0.4, 0.5, 0.6)
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("buffered samples leaked to the sink before the tick: %d", n)
	}

	end := t0.Add(60 * time.Second)
	f.flush(t, 60*time.Second, end)

	ms := f.allMetrics()
	if len(ms) != 1 {
		t.Fatalf("got %d metrics, want 1", len(ms))
	}
	m := ms[0]
	if m.Type() != pmetric.MetricTypeExponentialHistogram {
		t.Fatalf("type = %v, want exponential histogram", m.Type())
	}
	if m.Name() != "cpu.util" || m.Unit() != "1" {
		t.Errorf("name/unit = %q/%q, want cpu.util/1", m.Name(), m.Unit())
	}
	eh := m.ExponentialHistogram()
	if eh.AggregationTemporality() != pmetric.AggregationTemporalityDelta {
		t.Errorf("temporality = %v, want delta", eh.AggregationTemporality())
	}
	if eh.DataPoints().Len() != 1 {
		t.Fatalf("got %d points, want 1", eh.DataPoints().Len())
	}
	dp := eh.DataPoints().At(0)
	if dp.Count() != 6 {
		t.Errorf("count = %d, want 6", dp.Count())
	}
	if dp.Min() != 0.1 || dp.Max() != 0.6 {
		t.Errorf("min/max = %v/%v, want 0.1/0.6", dp.Min(), dp.Max())
	}
	if math.Abs(dp.Sum()-2.1) > 1e-9 {
		t.Errorf("sum = %v, want 2.1", dp.Sum())
	}
	if got := dp.StartTimestamp().AsTime(); !got.Equal(t0) {
		t.Errorf("start = %v, want %v", got, t0)
	}
	if got := dp.Timestamp().AsTime(); !got.Equal(end) {
		t.Errorf("end = %v, want %v", got, end)
	}
	var inBuckets uint64
	for i := 0; i < dp.Positive().BucketCounts().Len(); i++ {
		inBuckets += dp.Positive().BucketCounts().At(i)
	}
	if inBuckets+dp.ZeroCount() != 6 {
		t.Errorf("bucket counts + zero count = %d, want 6", inBuckets+dp.ZeroCount())
	}
}

func TestHistogramKeepsResourceAndAttributes(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 0.5)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	rms := f.sink.AllMetrics()[0].ResourceMetrics()
	host, _ := rms.At(0).Resource().Attributes().Get("host.name")
	if host.Str() != "h1" {
		t.Errorf("host.name = %q, want h1", host.Str())
	}
	if name := rms.At(0).ScopeMetrics().At(0).Scope().Name(); name != "test" {
		t.Errorf("scope = %q, want test", name)
	}
}

func TestWindowResetsAfterFlush(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 1, 2, 3)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 9, t0.Add(70 * time.Second)}))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second))

	ms := f.allMetrics()
	if len(ms) != 2 {
		t.Fatalf("got %d metrics, want 2", len(ms))
	}
	second := ms[1].ExponentialHistogram().DataPoints().At(0)
	if second.Count() != 1 || second.Sum() != 9 {
		t.Errorf("second window count/sum = %d/%v, want 1/9", second.Count(), second.Sum())
	}
	if got := second.StartTimestamp().AsTime(); !got.Equal(t0.Add(60 * time.Second)) {
		t.Errorf("second window start = %v, want %v", got, t0.Add(60*time.Second))
	}
}

func TestCumulativeTemporalityAccumulatesAcrossWindows(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"cpu": {Temporality: TemporalityCumulative, Metrics: []string{"cpu.util"}},
	})
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, t0.Add(10 * time.Second)}))
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 5, t0.Add(70 * time.Second)}))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second))

	ms := f.allMetrics()
	if len(ms) != 2 {
		t.Fatalf("got %d metrics, want 2", len(ms))
	}
	eh := ms[1].ExponentialHistogram()
	if eh.AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
		t.Errorf("temporality = %v, want cumulative", eh.AggregationTemporality())
	}
	dp := eh.DataPoints().At(0)
	if dp.Count() != 2 || dp.Sum() != 6 {
		t.Errorf("count/sum = %d/%v, want 2/6", dp.Count(), dp.Sum())
	}
	if got := dp.StartTimestamp().AsTime(); !got.Equal(t0) {
		t.Errorf("start = %v, want the series first-seen time %v", got, t0)
	}
}

func TestSeriesWithNoSamplesEmitsNothing(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", 1)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	before := f.sink.DataPointCount()

	f.flush(t, 60*time.Second, t0.Add(120*time.Second))
	if after := f.sink.DataPointCount(); after != before {
		t.Fatalf("empty window emitted %d points", after-before)
	}
}

func TestInvalidSamplesDiscarded(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	feedWindow(t, f, "cpu.util", math.NaN(), math.Inf(1), math.Inf(-1), 4)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	dp := f.allMetrics()[0].ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 1 || dp.Sum() != 4 {
		t.Errorf("count/sum = %d/%v, want 1/4", dp.Count(), dp.Sum())
	}
	if got := f.proc.discarded(reasonInvalid); got != 3 {
		t.Errorf("invalid discards = %d, want 3", got)
	}
}

func TestLateSamplesDiscarded(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	f.flush(t, 60*time.Second, t0.Add(60*time.Second)) // window now starts at t0+60s

	tooOld := t0.Add(-1 * time.Second) // older than window start (t0+60s) minus 60s
	onTime := t0.Add(1 * time.Second)  // inside the allowed lag
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, tooOld}, sample{"cpu.util", 2, onTime}))
	f.flush(t, 60*time.Second, t0.Add(120*time.Second))

	dp := f.allMetrics()[0].ExponentialHistogram().DataPoints().At(0)
	if dp.Count() != 1 || dp.Sum() != 2 {
		t.Errorf("count/sum = %d/%v, want 1/2", dp.Count(), dp.Sum())
	}
	if got := f.proc.discarded(reasonLate); got != 1 {
		t.Errorf("late discards = %d, want 1", got)
	}
}

func TestPlainGroupEmitsLastValueOncePerWindow(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"process": {Metrics: []string{"load"}}})
	for i := 1; i <= 6; i++ {
		f.consume(t, counterBatch("load", int64(i*100), t0.Add(time.Duration(i)*10*time.Second)))
	}
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("plain samples leaked before the tick: %d", n)
	}
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	ms := f.allMetrics()
	if len(ms) != 1 {
		t.Fatalf("got %d metrics, want 1", len(ms))
	}
	sum := ms[0].Sum()
	if !sum.IsMonotonic() || sum.AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
		t.Errorf("counter shape changed: monotonic=%v temporality=%v", sum.IsMonotonic(), sum.AggregationTemporality())
	}
	if sum.DataPoints().Len() != 1 {
		t.Fatalf("got %d points, want 1", sum.DataPoints().Len())
	}
	dp := sum.DataPoints().At(0)
	if dp.IntValue() != 600 {
		t.Errorf("value = %d, want the last scraped value 600", dp.IntValue())
	}
	if got := dp.Timestamp().AsTime(); !got.Equal(t0.Add(60 * time.Second)) {
		t.Errorf("timestamp = %v, want the sample's own %v", got, t0.Add(60*time.Second))
	}
}

func TestPlainGroupAtScrapeEqualsSendPassesThroughEqual(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"process": {ScrapeInterval: 10 * time.Second, SendInterval: 10 * time.Second, Metrics: []string{"load"}},
	})
	in := counterBatch("load", 7, t0.Add(10*time.Second))
	want := marshal(t, in)
	f.consume(t, in)

	got := f.sink.AllMetrics()
	if len(got) != 1 {
		t.Fatalf("sink got %d batches, want 1 at once", len(got))
	}
	if marshal(t, got[0]) != want {
		t.Errorf("plain pass-through changed the data:\n got %s\nwant %s", marshal(t, got[0]), want)
	}
}

func TestUngroupedMetricPassesThroughEqual(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	in := gaugeBatch("h1", sample{"other.metric", 3, t0.Add(10 * time.Second)})
	want := marshal(t, in)
	f.consume(t, in)

	got := f.sink.AllMetrics()
	if len(got) != 1 || marshal(t, got[0]) != want {
		t.Fatalf("ungrouped metric was altered or delayed: %d batches", len(got))
	}
}

func TestMixedBatchSplitsGroupedFromPassThrough(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	f.consume(t, gaugeBatch("h1",
		sample{"cpu.util", 1, t0.Add(10 * time.Second)},
		sample{"other.metric", 3, t0.Add(10 * time.Second)},
	))

	ms := f.allMetrics()
	if len(ms) != 1 || ms[0].Name() != "other.metric" {
		t.Fatalf("pass-through = %d metrics, want only other.metric", len(ms))
	}
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))
	if got := f.allMetrics(); len(got) != 2 || got[1].Name() != "cpu.util" {
		t.Fatalf("after tick got %d metrics, want other.metric then cpu.util", len(got))
	}
}

func TestMonotonicSumInHistogramGroupPassesThrough(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"network_rate": {Metrics: []string{"net.io"}}})
	f.consume(t, counterBatch("net.io", 5, t0.Add(10*time.Second)))
	if n := f.sink.DataPointCount(); n != 1 {
		t.Fatalf("counter in a histogram group should pass through until rates are built; sink has %d points", n)
	}
}

func TestSeriesAreKeyedByAttributes(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}})
	f.consume(t, gaugeBatch("h1", sample{"cpu.util", 1, t0.Add(10 * time.Second)}))
	f.consume(t, gaugeBatch("h2", sample{"cpu.util", 2, t0.Add(10 * time.Second)}))
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	if got := f.sink.DataPointCount(); got != 2 {
		t.Fatalf("got %d points, want one per host", got)
	}
}

func TestFlushOnlyTouchesSeriesOfThatSendInterval(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{
		"cpu":    {Metrics: []string{"cpu.util"}},
		"memory": {SendInterval: 120 * time.Second, Metrics: []string{"mem.util"}},
	})
	f.consume(t, gaugeBatch("h1",
		sample{"cpu.util", 1, t0.Add(10 * time.Second)},
		sample{"mem.util", 2, t0.Add(10 * time.Second)},
	))
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	ms := f.allMetrics()
	if len(ms) != 1 || ms[0].Name() != "cpu.util" {
		t.Fatalf("60s tick emitted %d metrics, want only cpu.util", len(ms))
	}
	f.flush(t, 120*time.Second, t0.Add(120*time.Second))
	if got := f.allMetrics(); len(got) != 2 || got[1].Name() != "mem.util" {
		t.Fatalf("120s tick did not emit mem.util")
	}
}

func TestLifecycleTicksAndShutdown(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.ScrapeInterval = 5 * time.Millisecond
	cfg.SendInterval = 10 * time.Millisecond
	cfg.Groups = map[string]GroupConfig{"cpu": {Metrics: []string{"cpu.util"}}}
	sink := new(consumertest.MetricsSink)
	proc, err := newWindowProcessor(cfg, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Shutdown(context.Background()) })
	if err := proc.ConsumeMetrics(context.Background(), gaugeBatch("h1", sample{"cpu.util", 1, time.Now()})); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for sink.DataPointCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := proc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if sink.DataPointCount() == 0 {
		t.Fatal("the ticker never flushed a window")
	}
}

func TestCapabilitiesMutateData(t *testing.T) {
	f := newFixture(t, nil)
	if !f.proc.Capabilities().MutatesData {
		t.Error("the processor removes buffered metrics from the batch, so MutatesData must be true")
	}
}

func marshal(t *testing.T, md pmetric.Metrics) string {
	t.Helper()
	b, err := new(pmetric.JSONMarshaler).MarshalMetrics(md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
