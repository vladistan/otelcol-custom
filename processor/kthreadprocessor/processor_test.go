// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var (
	scrapeStart = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	scrapeAt    = scrapeStart.Add(60 * time.Second)
)

// thread is one process of a host scrape: cpu seconds per state and disk bytes per direction.
type thread struct {
	name         string
	pid          int64
	owner        string
	user, system float64
	read, write  int64
	start, at    time.Time
}

func (th thread) times() (time.Time, time.Time) {
	start, at := th.start, th.at
	if start.IsZero() {
		start = scrapeStart
	}
	if at.IsZero() {
		at = scrapeAt
	}
	return start, at
}

// scrape builds one host batch the way the process scraper does: one resource per
// process, one scope, a cumulative process.cpu.time and a cumulative process.disk.io.
func scrape(threads ...thread) pmetric.Metrics {
	md := pmetric.NewMetrics()
	for _, th := range threads {
		rm := md.ResourceMetrics().AppendEmpty()
		attrs := rm.Resource().Attributes()
		attrs.PutInt("process.pid", th.pid)
		attrs.PutStr("process.executable.name", th.name)
		if th.owner != "" {
			attrs.PutStr("process.owner", th.owner)
		}
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName("hostmetrics")
		start, at := th.times()

		cpu := sm.Metrics().AppendEmpty()
		cpu.SetName("process.cpu.time")
		cpu.SetDescription("Total CPU seconds broken down by different states.")
		cpu.SetUnit("s")
		cpu.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		cpu.Sum().SetIsMonotonic(true)
		for state, v := range map[string]float64{"user": th.user, "system": th.system} {
			dp := cpu.Sum().DataPoints().AppendEmpty()
			dp.Attributes().PutStr("state", state)
			dp.SetDoubleValue(v)
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
			dp.SetTimestamp(pcommon.NewTimestampFromTime(at))
		}

		io := sm.Metrics().AppendEmpty()
		io.SetName("process.disk.io")
		io.SetUnit("By")
		io.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		io.Sum().SetIsMonotonic(true)
		for direction, v := range map[string]int64{"read": th.read, "write": th.write} {
			dp := io.Sum().DataPoints().AppendEmpty()
			dp.Attributes().PutStr("direction", direction)
			dp.SetIntValue(v)
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
			dp.SetTimestamp(pcommon.NewTimestampFromTime(at))
		}
	}
	return md
}

func defaultConfig() *Config { return createDefaultConfig().(*Config) }

func run(t *testing.T, cfg *Config, in pmetric.Metrics) pmetric.Metrics {
	t.Helper()
	sink := new(consumertest.MetricsSink)
	p, err := newProcessor(cfg, sink)
	require.NoError(t, err)

	require.NoError(t, p.ConsumeMetrics(context.Background(), in))

	require.Len(t, sink.AllMetrics(), 1)
	return sink.AllMetrics()[0]
}

func countPoints(md pmetric.Metrics) int {
	total := 0
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				total += ms.At(k).Sum().DataPoints().Len()
			}
		}
	}
	return total
}

func executableName(rm pmetric.ResourceMetrics) string {
	v, _ := rm.Resource().Attributes().Get("process.executable.name")
	return v.Str()
}

func resourcesNamed(md pmetric.Metrics, name string) []pmetric.ResourceMetrics {
	var out []pmetric.ResourceMetrics
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		if rm := md.ResourceMetrics().At(i); executableName(rm) == name {
			out = append(out, rm)
		}
	}
	return out
}

func metricNamed(t *testing.T, rm pmetric.ResourceMetrics, name string) pmetric.Metric {
	t.Helper()
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		ms := rm.ScopeMetrics().At(i).Metrics()
		for j := 0; j < ms.Len(); j++ {
			if ms.At(j).Name() == name {
				return ms.At(j)
			}
		}
	}
	require.Failf(t, "metric not found", "%s", name)
	return pmetric.Metric{}
}

func pointWith(t *testing.T, m pmetric.Metric, key, value string) pmetric.NumberDataPoint {
	t.Helper()
	dps := m.Sum().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		if v, ok := dps.At(i).Attributes().Get(key); ok && v.Str() == value {
			return dps.At(i)
		}
	}
	require.Failf(t, "data point not found", "%s=%s", key, value)
	return pmetric.NumberDataPoint{}
}

// snapshot renders one resource so before and after can be compared byte for byte.
func snapshot(t *testing.T, rm pmetric.ResourceMetrics) string {
	t.Helper()
	one := pmetric.NewMetrics()
	rm.CopyTo(one.ResourceMetrics().AppendEmpty())
	data, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(one)
	require.NoError(t, err)
	return string(data)
}

// hostFixture is 100 processes with 72 kernel threads, the share measured on the real hosts.
func hostFixture() (pmetric.Metrics, []thread) {
	var threads []thread
	pid := int64(100)
	add := func(name string) {
		pid++
		threads = append(threads, thread{name: name, pid: pid, owner: "root", user: float64(pid) / 10, system: 1, read: pid * 10, write: pid})
	}
	for i := 0; i < 40; i++ {
		add(fmt.Sprintf("kworker/%d:%dH-kblockd", i%8, i))
	}
	for i := 0; i < 16; i++ {
		add(fmt.Sprintf("ksoftirqd/%d", i))
	}
	for i := 0; i < 16; i++ {
		add(fmt.Sprintf("migration/%d", i))
	}
	for i := 0; i < 28; i++ {
		add(fmt.Sprintf("userproc%d", i))
	}
	return scrape(threads...), threads
}

func TestConsumeMetricsKernelThreadSeriesFallByAtLeast65Percent(t *testing.T) {
	in, _ := hostFixture()
	before := countPoints(in)

	out := run(t, defaultConfig(), in)

	after := countPoints(out)
	assert.Equal(t, 400, before)
	assert.LessOrEqual(t, float64(after), 0.35*float64(before), "series after %d, before %d", after, before)
	assert.Equal(t, 28+1, out.ResourceMetrics().Len(), "28 user processes and one aggregate")
}

func TestConsumeMetricsNonMatchingResourcesAreUnchanged(t *testing.T) {
	in, threads := hostFixture()
	want := map[string]string{}
	for i := 0; i < in.ResourceMetrics().Len(); i++ {
		rm := in.ResourceMetrics().At(i)
		if name := executableName(rm); len(name) >= 8 && name[:8] == "userproc" {
			want[name] = snapshot(t, rm)
		}
	}
	require.Len(t, threads, 100)

	out := run(t, defaultConfig(), in)

	got := map[string]string{}
	for i := 0; i < out.ResourceMetrics().Len(); i++ {
		rm := out.ResourceMetrics().At(i)
		if name := executableName(rm); name != "kernel_threads" {
			got[name] = snapshot(t, rm)
		}
	}
	assert.Equal(t, want, got)
}

func TestConsumeMetricsAggregateEqualsTheSumOfTheScrape(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	p, err := newProcessor(defaultConfig(), sink)
	require.NoError(t, err)
	first := scrape(
		thread{name: "kworker/0:1", pid: 11, user: 1.5, system: 0.5, read: 100, write: 10},
		thread{name: "ksoftirqd/0", pid: 12, user: 2, system: 1, read: 50, write: 5},
		thread{name: "migration/0", pid: 13, user: 0.25, system: 0.25},
	)
	second := scrape(
		thread{name: "kworker/0:1", pid: 11, user: 1.5, system: 0.5, read: 100, write: 10},
		thread{name: "migration/0", pid: 13, user: 0.25, system: 0.25},
	)

	require.NoError(t, p.ConsumeMetrics(context.Background(), first))
	require.NoError(t, p.ConsumeMetrics(context.Background(), second))

	require.Len(t, sink.AllMetrics(), 2)
	for i, want := range []struct {
		user, system float64
		read, write  int64
	}{{3.75, 1.75, 150, 15}, {1.75, 0.75, 100, 10}} {
		aggs := resourcesNamed(sink.AllMetrics()[i], "kernel_threads")
		require.Len(t, aggs, 1, "scrape %d", i)
		cpu := metricNamed(t, aggs[0], "process.cpu.time")
		io := metricNamed(t, aggs[0], "process.disk.io")
		assert.InDelta(t, want.user, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9, "scrape %d user", i)
		assert.InDelta(t, want.system, pointWith(t, cpu, "state", "system").DoubleValue(), 1e-9, "scrape %d system", i)
		assert.Equal(t, want.read, pointWith(t, io, "direction", "read").IntValue(), "scrape %d read", i)
		assert.Equal(t, want.write, pointWith(t, io, "direction", "write").IntValue(), "scrape %d write", i)
	}
}

func TestConsumeMetricsAggregateResourceHasNoPidAndTheConfiguredName(t *testing.T) {
	in := scrape(
		thread{name: "kworker/0:1", pid: 11, owner: "root"},
		thread{name: "ksoftirqd/0", pid: 12, owner: "root"},
	)
	cfg := defaultConfig()
	cfg.AggregateName = "kthreads_total"

	out := run(t, cfg, in)

	require.Equal(t, 1, out.ResourceMetrics().Len())
	attrs := out.ResourceMetrics().At(0).Resource().Attributes()
	_, hasPid := attrs.Get("process.pid")
	name, _ := attrs.Get("process.executable.name")
	owner, hasOwner := attrs.Get("process.owner")
	assert.False(t, hasPid)
	assert.Equal(t, "kthreads_total", name.Str())
	assert.True(t, hasOwner, "an attribute every merged resource shares is kept")
	assert.Equal(t, "root", owner.Str())
}

func TestConsumeMetricsAggregateDropsAttributesTheMergedResourcesDisagreeOn(t *testing.T) {
	in := scrape(
		thread{name: "kworker/0:1", pid: 11, owner: "root"},
		thread{name: "ksoftirqd/0", pid: 12, owner: "nobody"},
	)

	out := run(t, defaultConfig(), in)

	_, hasOwner := out.ResourceMetrics().At(0).Resource().Attributes().Get("process.owner")
	assert.False(t, hasOwner)
}

func TestConsumeMetricsAggregateDropsPerProcessIdentityEvenForOneThread(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 11, owner: "root"})
	attrs := in.ResourceMetrics().At(0).Resource().Attributes()
	attrs.PutStr("process.command_line", "kworker/0:1")
	attrs.PutStr("process.executable.path", "/proc/11/exe")
	attrs.PutInt("process.parent_pid", 2)
	attrs.PutStr("host.name", "edge-1")

	out := run(t, defaultConfig(), in)

	got := out.ResourceMetrics().At(0).Resource().Attributes().AsRaw()
	assert.Equal(t, map[string]any{
		"process.executable.name": "kernel_threads",
		"process.owner":           "root",
		"host.name":               "edge-1",
	}, got)
}

func TestConsumeMetricsUserProcessMatchingAPatternIsAggregated(t *testing.T) {
	in := scrape(
		thread{name: "migration-tool", pid: 21, user: 4, system: 1},
		thread{name: "migration/0", pid: 22, user: 1, system: 1},
	)

	out := run(t, defaultConfig(), in)

	require.Equal(t, 1, out.ResourceMetrics().Len())
	cpu := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	assert.InDelta(t, 5.0, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9)
}

func TestConsumeMetricsPatternListIsConfigurable(t *testing.T) {
	in := scrape(
		thread{name: "rcu_preempt", pid: 31, user: 2},
		thread{name: "rcu_sched", pid: 32, user: 3},
		thread{name: "kworker/0:1", pid: 33, user: 9},
	)
	cfg := &Config{Patterns: []string{"rcu_*"}, AggregateName: "rcu_total"}

	out := run(t, cfg, in)

	require.Len(t, resourcesNamed(out, "kworker/0:1"), 1, "no longer matched, so unchanged")
	aggs := resourcesNamed(out, "rcu_total")
	require.Len(t, aggs, 1)
	cpu := metricNamed(t, aggs[0], "process.cpu.time")
	assert.InDelta(t, 5.0, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9)
}

func TestConsumeMetricsResourceWithoutAnExecutableNameIsUnchanged(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 41})
	noName := scrape(thread{name: "x", pid: 42})
	noName.ResourceMetrics().At(0).Resource().Attributes().Remove("process.executable.name")
	wrongType := scrape(thread{name: "x", pid: 43})
	wrongType.ResourceMetrics().At(0).Resource().Attributes().PutInt("process.executable.name", 7)
	noName.ResourceMetrics().MoveAndAppendTo(in.ResourceMetrics())
	wrongType.ResourceMetrics().MoveAndAppendTo(in.ResourceMetrics())
	want := []string{snapshot(t, in.ResourceMetrics().At(1)), snapshot(t, in.ResourceMetrics().At(2))}

	out := run(t, defaultConfig(), in)

	require.Equal(t, 3, out.ResourceMetrics().Len())
	assert.Equal(t, want, []string{snapshot(t, out.ResourceMetrics().At(0)), snapshot(t, out.ResourceMetrics().At(1))})
}

func TestConsumeMetricsOutputKeepsOtherResourcesFirstAndTheAggregateLast(t *testing.T) {
	in := scrape(
		thread{name: "kworker/0:1", pid: 1},
		thread{name: "sshd", pid: 2},
		thread{name: "ksoftirqd/0", pid: 3},
		thread{name: "nginx", pid: 4},
	)

	out := run(t, defaultConfig(), in)

	var names []string
	for i := 0; i < out.ResourceMetrics().Len(); i++ {
		names = append(names, executableName(out.ResourceMetrics().At(i)))
	}
	assert.Equal(t, []string{"sshd", "nginx", "kernel_threads"}, names)
}

func TestConsumeMetricsBatchWithNoKernelThreadsEmitsNoAggregate(t *testing.T) {
	in := scrape(thread{name: "sshd", pid: 2}, thread{name: "nginx", pid: 4})

	out := run(t, defaultConfig(), in)

	assert.Empty(t, resourcesNamed(out, "kernel_threads"))
	assert.Equal(t, 2, out.ResourceMetrics().Len())
}

func TestConsumeMetricsEmptyBatchPassesThrough(t *testing.T) {
	out := run(t, defaultConfig(), pmetric.NewMetrics())

	assert.Equal(t, 0, out.ResourceMetrics().Len())
}

func TestConsumeMetricsCumulativeMonotonicSumBecomesNonMonotonic(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1}, thread{name: "kworker/0:2", pid: 2, user: 1})

	out := run(t, defaultConfig(), in)

	cpu := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	assert.Equal(t, pmetric.MetricTypeSum, cpu.Type())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, cpu.Sum().AggregationTemporality())
	assert.False(t, cpu.Sum().IsMonotonic(), "a per-scrape sum falls when a thread exits")
	assert.Equal(t, "s", cpu.Unit())
	assert.Equal(t, "Total CPU seconds broken down by different states.", cpu.Description())
}

func TestConsumeMetricsDeltaSumStaysMonotonic(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1})
	cpu := metricNamed(t, in.ResourceMetrics().At(0), "process.cpu.time")
	cpu.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)

	out := run(t, defaultConfig(), in)

	got := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	assert.Equal(t, pmetric.AggregationTemporalityDelta, got.Sum().AggregationTemporality())
	assert.True(t, got.Sum().IsMonotonic())
}

func TestConsumeMetricsGaugesAreSummed(t *testing.T) {
	in := pmetric.NewMetrics()
	for i, v := range []float64{0.25, 0.5} {
		rm := in.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("process.executable.name", "kworker/0:1")
		rm.Resource().Attributes().PutInt("process.pid", int64(i))
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("process.cpu.utilization")
		m.SetUnit("1")
		m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(v)
	}

	out := run(t, defaultConfig(), in)

	m := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.utilization")
	require.Equal(t, pmetric.MetricTypeGauge, m.Type())
	require.Equal(t, 1, m.Gauge().DataPoints().Len())
	assert.InDelta(t, 0.75, m.Gauge().DataPoints().At(0).DoubleValue(), 1e-9)
}

func TestConsumeMetricsMixedIntAndDoublePointsSumAsDouble(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1}, thread{name: "kworker/0:2", pid: 2})
	cpu := metricNamed(t, in.ResourceMetrics().At(0), "process.cpu.time")
	for i := 0; i < cpu.Sum().DataPoints().Len(); i++ {
		dp := cpu.Sum().DataPoints().At(i)
		if v, _ := dp.Attributes().Get("state"); v.Str() == "user" {
			dp.SetIntValue(2)
		}
	}
	other := metricNamed(t, in.ResourceMetrics().At(1), "process.cpu.time")
	for i := 0; i < other.Sum().DataPoints().Len(); i++ {
		dp := other.Sum().DataPoints().At(i)
		if v, _ := dp.Attributes().Get("state"); v.Str() == "user" {
			dp.SetDoubleValue(0.5)
		}
	}

	out := run(t, defaultConfig(), in)

	got := pointWith(t, metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time"), "state", "user")
	assert.Equal(t, pmetric.NumberDataPointValueTypeDouble, got.ValueType())
	assert.InDelta(t, 2.5, got.DoubleValue(), 1e-9)
}

func TestConsumeMetricsAggregatePointTakesTheEarliestStartOfTheScrape(t *testing.T) {
	early := scrapeStart.Add(-time.Hour)
	in := scrape(
		thread{name: "kworker/0:1", pid: 1, start: scrapeStart},
		thread{name: "kworker/0:2", pid: 2, start: early},
		thread{name: "kworker/0:3", pid: 3, start: scrapeStart},
	)

	out := run(t, defaultConfig(), in)

	dp := pointWith(t, metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time"), "state", "user")
	assert.Equal(t, pcommon.NewTimestampFromTime(early), dp.StartTimestamp())
	assert.Equal(t, pcommon.NewTimestampFromTime(scrapeAt), dp.Timestamp())
}

func TestConsumeMetricsSummaryAndHistogramMetricsAreLeftOutOfTheAggregate(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1})
	hist := in.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().AppendEmpty()
	hist.SetName("process.latency")
	hist.SetEmptyHistogram().DataPoints().AppendEmpty().SetCount(3)

	out := run(t, defaultConfig(), in)

	ms := out.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		assert.NotEqual(t, "process.latency", ms.At(i).Name())
	}
	assert.Equal(t, 2, ms.Len())
}

func TestConsumeMetricsAggregateKeepsEachScopeSeparate(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1}, thread{name: "kworker/0:2", pid: 2, user: 1})
	in.ResourceMetrics().At(1).ScopeMetrics().At(0).Scope().SetName("other")

	out := run(t, defaultConfig(), in)

	sms := out.ResourceMetrics().At(0).ScopeMetrics()
	require.Equal(t, 2, sms.Len())
	assert.ElementsMatch(t, []string{"hostmetrics", "other"}, []string{sms.At(0).Scope().Name(), sms.At(1).Scope().Name()})
}

func TestConsumeMetricsTwoScrapesInOneBatchStaySeparatePoints(t *testing.T) {
	t1, t2 := scrapeAt, scrapeAt.Add(time.Minute)
	in := scrape(thread{name: "kworker/0:1", pid: 11, user: 10, at: t1})
	later := scrape(thread{name: "kworker/0:1", pid: 11, user: 12, at: t2})
	later.ResourceMetrics().MoveAndAppendTo(in.ResourceMetrics())

	out := run(t, defaultConfig(), in)

	require.Equal(t, 1, out.ResourceMetrics().Len())
	got := map[pcommon.Timestamp]float64{}
	dps := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time").Sum().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		if v, _ := dps.At(i).Attributes().Get("state"); v.Str() == "user" {
			got[dps.At(i).Timestamp()] = dps.At(i).DoubleValue()
		}
	}
	assert.Equal(t, map[pcommon.Timestamp]float64{
		pcommon.NewTimestampFromTime(t1): 10,
		pcommon.NewTimestampFromTime(t2): 12,
	}, got)
}

// jittered builds n kworkers whose timestamps are 200 microseconds apart, as the
// process scraper stamps each process when it reads it.
func jittered(n int, at time.Time, firstPid int64) pmetric.Metrics {
	threads := make([]thread, n)
	for i := range threads {
		threads[i] = thread{name: fmt.Sprintf("kworker/0:%d", i), pid: firstPid + int64(i), user: 1, system: 1, read: 1, write: 1, at: at.Add(time.Duration(i) * 200 * time.Microsecond)}
	}
	return scrape(threads...)
}

func TestConsumeMetricsJitteredTimestampsOfOneScrapeMergeIntoOnePointPerSeries(t *testing.T) {
	in := jittered(50, scrapeAt, 100)

	out := run(t, defaultConfig(), in)

	assert.Equal(t, 4, countPoints(out), "user and system cpu.time, read and write disk.io")
	cpu := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	assert.InDelta(t, 50.0, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9)
	assert.Equal(t, pcommon.NewTimestampFromTime(scrapeAt.Add(49*200*time.Microsecond)), pointWith(t, cpu, "state", "user").Timestamp(), "the latest time of the scrape")
}

func TestConsumeMetricsJitteredScrapesTenSecondsApartStaySeparate(t *testing.T) {
	in := jittered(50, scrapeAt, 100)
	jittered(50, scrapeAt.Add(10*time.Second), 100).ResourceMetrics().MoveAndAppendTo(in.ResourceMetrics())

	out := run(t, defaultConfig(), in)

	assert.Equal(t, 8, countPoints(out), "four series in each of two scrapes")
	cpu := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	for i := 0; i < cpu.Sum().DataPoints().Len(); i++ {
		assert.InDelta(t, 50.0, cpu.Sum().DataPoints().At(i).DoubleValue(), 1e-9)
	}
}

func TestConsumeMetricsAggregateKeepsTheResourceSchemaURLTheThreadsShare(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1}, thread{name: "kworker/0:2", pid: 2})
	in.ResourceMetrics().At(0).SetSchemaUrl("https://opentelemetry.io/schemas/1.26.0")
	in.ResourceMetrics().At(1).SetSchemaUrl("https://opentelemetry.io/schemas/1.26.0")

	out := run(t, defaultConfig(), in)

	assert.Equal(t, "https://opentelemetry.io/schemas/1.26.0", out.ResourceMetrics().At(0).SchemaUrl())
}

func TestConsumeMetricsAggregateDropsASchemaURLTheThreadsDisagreeOn(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1}, thread{name: "kworker/0:2", pid: 2})
	in.ResourceMetrics().At(0).SetSchemaUrl("https://opentelemetry.io/schemas/1.26.0")

	out := run(t, defaultConfig(), in)

	assert.Empty(t, out.ResourceMetrics().At(0).SchemaUrl())
}

func TestConsumeMetricsStaleMarkerPointIsNotSummed(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1}, thread{name: "kworker/0:2", pid: 2, user: 4})
	stale := pointWith(t, metricNamed(t, in.ResourceMetrics().At(1), "process.cpu.time"), "state", "user")
	stale.SetFlags(stale.Flags().WithNoRecordedValue(true))

	out := run(t, defaultConfig(), in)

	cpu := metricNamed(t, out.ResourceMetrics().At(0), "process.cpu.time")
	assert.InDelta(t, 1.0, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9)
}

func TestConsumeMetricsMetricWithOnlyStaleMarkersIsLeftOut(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1})
	io := metricNamed(t, in.ResourceMetrics().At(0), "process.disk.io")
	for i := 0; i < io.Sum().DataPoints().Len(); i++ {
		dp := io.Sum().DataPoints().At(i)
		dp.SetFlags(dp.Flags().WithNoRecordedValue(true))
	}

	out := run(t, defaultConfig(), in)

	ms := out.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	require.Equal(t, 1, ms.Len())
	assert.Equal(t, "process.cpu.time", ms.At(0).Name())
}

func TestConsumeMetricsUptimeIsNotSummedIntoTheAggregate(t *testing.T) {
	in := scrape(thread{name: "kworker/0:1", pid: 1, user: 1}, thread{name: "kworker/0:2", pid: 2, user: 1})
	for i := 0; i < in.ResourceMetrics().Len(); i++ {
		m := in.ResourceMetrics().At(i).ScopeMetrics().At(0).Metrics().AppendEmpty()
		m.SetName("process.uptime")
		m.SetUnit("s")
		m.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue([]float64{600, 60}[i])
	}

	out := run(t, defaultConfig(), in)

	ms := out.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		assert.NotEqual(t, "process.uptime", ms.At(i).Name())
	}
	assert.Equal(t, 2, ms.Len(), "process.cpu.time and process.disk.io remain")
}

func TestConsumeMetricsReturnsTheNextConsumersErrorAsIs(t *testing.T) {
	boom := errors.New("downstream refused")
	p, err := newProcessor(defaultConfig(), consumertest.NewErr(boom))
	require.NoError(t, err)

	got := p.ConsumeMetrics(context.Background(), scrape(thread{name: "kworker/0:1", pid: 1}))

	assert.Same(t, boom, got)
}

func TestProcessorLifecycleAndCapabilities(t *testing.T) {
	p, err := newProcessor(defaultConfig(), new(consumertest.MetricsSink))
	require.NoError(t, err)

	assert.NoError(t, p.Start(context.Background(), nil))
	assert.NoError(t, p.Shutdown(context.Background()))
	assert.True(t, p.Capabilities().MutatesData)
}

func TestMatchesTreatsStarAndQuestionMarkAsWildcardsOnly(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"kworker*", "kworker/0:1H-kblockd", true},
		{"kworker*", "xkworker", false},
		{"migration/?", "migration/3", true},
		{"migration/?", "migration/12", false},
		{"rcu_p.eempt", "rcu_preempt", false},
		{"rcu_p.eempt", "rcu_p.eempt", true},
		{"*", "anything/at:all", true},
		{"jbd2/sda1-8", "jbd2/sda1-8", true},
		{"jbd2/sda1-8", "jbd2/sda1-80", false},
		{"a[b]", "a[b]", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.name, func(t *testing.T) {
			p, err := newProcessor(&Config{Patterns: []string{tt.pattern}, AggregateName: "kernel_threads"}, new(consumertest.MetricsSink))
			require.NoError(t, err)

			assert.Equal(t, tt.want, p.matches(tt.name))
		})
	}
}
