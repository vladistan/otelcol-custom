// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// histogramGroups are the histogram groups; every other catalogue group is plain.
var histogramGroups = []string{
	"cpu", "memory", "disk_latency", "disk_io_rate", "network_rate",
	"cpu_pressure", "memory_pages", "network_pressure", "gpu",
}

var plainGroups = []string{"psi", "process", "filesystem", "plain"}

func groupNames(m map[string]catalogueEntry) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestCatalogueHasExactlyTheDefaultGroups(t *testing.T) {
	want := append(append([]string{}, histogramGroups...), plainGroups...)
	sort.Strings(want)
	if got := groupNames(catalogue); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("catalogue groups = %v, want %v", got, want)
	}
}

func TestGroupModes(t *testing.T) {
	for _, name := range histogramGroups {
		if catalogue[name].Mode != ModeHistogram {
			t.Errorf("%s should be a histogram group", name)
		}
	}
	for _, name := range plainGroups {
		if catalogue[name].Mode != ModePlain {
			t.Errorf("%s should be a plain group", name)
		}
	}
}

func TestEveryGroupHasMetrics(t *testing.T) {
	for name, entry := range catalogue {
		if len(entry.Metrics) == 0 {
			t.Errorf("group %s has no built-in metrics", name)
		}
	}
}

func TestNoMetricIsInTwoGroups(t *testing.T) {
	owner := map[string]string{}
	for name, entry := range catalogue {
		for _, metric := range entry.Metrics {
			if prev, dup := owner[metric]; dup {
				t.Errorf("%s is in both %s and %s", metric, prev, name)
			}
			owner[metric] = name
		}
	}
}

func TestPlainMetricsStayPlain(t *testing.T) {
	plain := []string{
		"windows.memory.committed", "windows.memory.commit_limit", "system.network.connections",
		"system.pressure.cpu.some.avg10", "system.pressure.io.full.avg300", "system.pressure.memory.some.avg60",
		"hw.gpu.io", "hw.errors",
		"system.paging.usage", "system.cpu.load_average.1m", "system.filesystem.usage", "process.cpu.time",
	}
	groups := map[string]GroupConfig{}
	for name := range catalogue {
		groups[name] = GroupConfig{}
	}
	f := newFixture(t, groups)
	for _, metric := range plain {
		f.consume(t, gaugeBatch("h1", sample{metric, 1, t0.Add(10 * time.Second)}))
	}
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("plain metrics should be held until the window closes; %d passed through", n)
	}
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	emitted := map[string]pmetric.MetricType{}
	for _, m := range f.allMetrics() {
		emitted[m.Name()] = m.Type()
	}
	for _, metric := range plain {
		typ, ok := emitted[metric]
		if !ok {
			t.Errorf("%s was not emitted under its own name", metric)
		} else if typ == pmetric.MetricTypeExponentialHistogram {
			t.Errorf("%s became a histogram, want it plain", metric)
		}
	}
}

// These metrics must be summarized: otherwise they pass through every scrape, six times a
// minute at a 10s scrape. They are derived by normalizedload, or emitted by receivers the
// catalogue did not list.
func TestDerivedAndExtraMetricsAreSummarized(t *testing.T) {
	cases := []struct {
		metric        string
		group         string
		wantHistogram bool
	}{
		{"system.cpu.utilization.percpu", "cpu", true},
		{"system.cpu.load_average.normalized.1m", "plain", false},
		{"system.cpu.load_average.normalized.5m", "plain", false},
		{"system.cpu.load_average.normalized.15m", "plain", false},
		{"process.cpu.utilization.percpu", "process", false},
		{"system.pressure.cpu.full.avg10", "psi", false},
		{"system.pressure.cpu.full.avg60", "psi", false},
		{"system.pressure.cpu.full.avg300", "psi", false},
		{"system.disk.io_time", "plain", false},
		{"system.disk.weighted_io_time", "plain", false},
		{"system.disk.operation_time", "plain", false},
		{"system.disk.merged", "plain", false},
	}
	groups := map[string]GroupConfig{}
	for name := range catalogue {
		groups[name] = GroupConfig{}
	}
	f := newFixture(t, groups)
	for _, c := range cases {
		f.consume(t, gaugeBatch("h1", sample{c.metric, 1, t0.Add(10 * time.Second)}))
	}
	if n := f.sink.DataPointCount(); n != 0 {
		t.Fatalf("%d points passed through before the window closed", n)
	}
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	emitted := map[string]pmetric.MetricType{}
	for _, m := range f.allMetrics() {
		emitted[m.Name()] = m.Type()
	}
	for _, c := range cases {
		typ, ok := emitted[c.metric]
		if !ok {
			t.Errorf("%s was not emitted under its own name", c.metric)
			continue
		}
		if isHist := typ == pmetric.MetricTypeExponentialHistogram; isHist != c.wantHistogram {
			t.Errorf("%s histogram = %v, want %v", c.metric, isHist, c.wantHistogram)
		}
	}
}

// counterMetrics arrive as cumulative counters; every other built-in metric is a gauge.
var counterMetrics = map[string]bool{
	"system.network.io": true, "system.network.packets": true, "system.network.errors": true,
	"system.network.dropped": true, "system.disk.io": true, "system.disk.operations": true,
}

// feedGroup sends enough samples of every metric in a group for it to emit.
func feedGroup(t *testing.T, f *fixture, entry catalogueEntry) {
	t.Helper()
	for _, metric := range entry.Metrics {
		if counterMetrics[metric] {
			f.feedCounter(t, metric, 10, 1000)
			f.feedCounter(t, metric, 20, 2000)
			continue
		}
		f.consume(t, gaugeBatch("h1", sample{metric, 1, t0.Add(10 * time.Second)}))
	}
}

func isHistogramGroup(name string) bool {
	for _, g := range histogramGroups {
		if g == name {
			return true
		}
	}
	return false
}

func TestDefaultGroupsEmitTheRightShape(t *testing.T) {
	for name, entry := range catalogue {
		wantHistogram := isHistogramGroup(name)
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, map[string]GroupConfig{name: {}})
			feedGroup(t, f, entry)
			f.flush(t, 60*time.Second, t0.Add(60*time.Second))

			emitted := map[string]pmetric.Metric{}
			for _, m := range f.allMetrics() {
				emitted[m.Name()] = m
			}
			for _, metric := range entry.Metrics {
				if !wantHistogram {
					m, ok := emitted[metric]
					if !ok {
						t.Errorf("%s: plain metric not emitted", metric)
						continue
					}
					if m.Type() == pmetric.MetricTypeExponentialHistogram {
						t.Errorf("%s: plain metric became a histogram", metric)
					}
					continue
				}
				histName := metric
				if counterMetrics[metric] {
					histName += ".rate"
				}
				m, ok := emitted[histName]
				if !ok {
					t.Errorf("%s: no histogram named %s", metric, histName)
					continue
				}
				if m.Type() != pmetric.MetricTypeExponentialHistogram {
					t.Errorf("%s: type %v, want exponential histogram", histName, m.Type())
				}
			}
		})
	}
}

func TestSummarizedMetricsKeepNameAndUnit(t *testing.T) {
	f := newFixture(t, map[string]GroupConfig{"cpu": {}, "network_rate": {}})
	f.consume(t, gaugeBatch("h1", sample{"system.cpu.utilization", 0.5, t0.Add(10 * time.Second)}))
	f.feedCounter(t, "system.network.io", 10, 100)
	f.feedCounter(t, "system.network.io", 20, 300)
	f.flush(t, 60*time.Second, t0.Add(60*time.Second))

	got := map[string]string{}
	for _, m := range f.allMetrics() {
		got[m.Name()] = m.Unit()
	}
	want := map[string]string{
		"system.cpu.utilization": "1",
		"system.network.io.rate": "By/s",
		"system.network.io":      "By", // keep_counter defaults on for counter groups
	}
	for name, unit := range want {
		if gotUnit, ok := got[name]; !ok || gotUnit != unit {
			t.Errorf("metric %q unit = %q (present=%v), want %q", name, gotUnit, ok, unit)
		}
	}
}

// buildSeries fills a processor with n one-sample series. The reader exposes its self-metrics.
func buildSeries(t testing.TB, n int) (*windowProcessor, *sdkmetric.ManualReader) {
	cfg := createDefaultConfig().(*Config)
	cfg.Groups = map[string]GroupConfig{"cpu": {}}
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	proc, err := newWindowProcessor(cfg, new(consumertest.MetricsSink), meter)
	if err != nil {
		t.Fatal(err)
	}
	proc.initWindows(t0)
	for i := 0; i < n; i++ {
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("host.name", "h1")
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("system.cpu.utilization")
		m.SetUnit("1")
		dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(0.5)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(t0.Add(10 * time.Second)))
		dp.Attributes().PutStr("cpu", fmt.Sprintf("cpu%d", i))
		dp.Attributes().PutStr("state", "user")
		if err := proc.ConsumeMetrics(context.Background(), md); err != nil {
			t.Fatal(err)
		}
	}
	return proc, reader
}

func heapAlloc() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

const (
	budgetSeries = 5000
	budgetBytes  = 60 << 20 // 60 MiB at 5,000 active series
)

func TestFiveThousandSeriesStayUnderBudget(t *testing.T) {
	before := heapAlloc()
	proc, reader := buildSeries(t, budgetSeries)
	after := heapAlloc()
	runtime.KeepAlive(proc)

	if got := readSelfMetric(t, reader, metricSeriesActive); got != budgetSeries {
		t.Fatalf("series = %d, want %d", got, budgetSeries)
	}
	used := after - before
	t.Logf("%d series use %d bytes (%d per series)", budgetSeries, used, used/budgetSeries)
	if used > budgetBytes {
		t.Errorf("%d series use %d MiB, budget is %d MiB", budgetSeries, used>>20, budgetBytes>>20)
	}
}

func BenchmarkBytesPerSeries(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		before := heapAlloc()
		b.StartTimer()
		proc, _ := buildSeries(b, budgetSeries)
		b.StopTimer()
		after := heapAlloc()
		runtime.KeepAlive(proc)
		b.ReportMetric(float64(after-before)/budgetSeries, "bytes/series")
	}
}
