// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package processlifetimeprocessor

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

var scrapeAt = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// proc is one process of a scrape. A nil uptime leaves process.uptime out; a nil
// cpu or io map leaves that metric out.
type proc struct {
	name   string
	pid    int64
	uptime *float64
	cpu    map[string]float64
	io     map[string]int64
}

func seconds(v float64) *float64 { return &v }

func ts(offset time.Duration) pcommon.Timestamp {
	return pcommon.NewTimestampFromTime(scrapeAt.Add(offset))
}

func addProc(md pmetric.Metrics, p proc) pmetric.ResourceMetrics {
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutInt("process.pid", p.pid)
	rm.Resource().Attributes().PutStr("process.executable.name", p.name)
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("hostmetrics")
	if p.uptime != nil {
		m := sm.Metrics().AppendEmpty()
		m.SetName("process.uptime")
		m.SetUnit("s")
		dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(*p.uptime)
		dp.SetTimestamp(ts(0))
	}
	if p.cpu != nil {
		m := sm.Metrics().AppendEmpty()
		m.SetName("process.cpu.time")
		m.SetUnit("s")
		m.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		m.Sum().SetIsMonotonic(true)
		for state, v := range p.cpu {
			dp := m.Sum().DataPoints().AppendEmpty()
			dp.Attributes().PutStr("state", state)
			dp.SetDoubleValue(v)
			dp.SetTimestamp(ts(time.Second))
		}
	}
	if p.io != nil {
		m := sm.Metrics().AppendEmpty()
		m.SetName("process.disk.io")
		m.SetUnit("By")
		m.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		m.Sum().SetIsMonotonic(true)
		for direction, v := range p.io {
			dp := m.Sum().DataPoints().AppendEmpty()
			dp.Attributes().PutStr("direction", direction)
			dp.SetIntValue(v)
			dp.SetTimestamp(ts(2 * time.Second))
		}
	}
	return rm
}

func scrape(procs ...proc) pmetric.Metrics {
	md := pmetric.NewMetrics()
	for _, p := range procs {
		addProc(md, p)
	}
	return md
}

func run(t *testing.T, in pmetric.Metrics) pmetric.Metrics {
	t.Helper()
	sink := new(consumertest.MetricsSink)
	p := newProcessor(sink)

	require.NoError(t, p.ConsumeMetrics(context.Background(), in))

	require.Len(t, sink.AllMetrics(), 1)
	return sink.AllMetrics()[0]
}

func findMetric(rm pmetric.ResourceMetrics, name string) (pmetric.Metric, pmetric.ScopeMetrics, bool) {
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		sm := rm.ScopeMetrics().At(i)
		for j := 0; j < sm.Metrics().Len(); j++ {
			if sm.Metrics().At(j).Name() == name {
				return sm.Metrics().At(j), sm, true
			}
		}
	}
	return pmetric.Metric{}, pmetric.ScopeMetrics{}, false
}

func metricCount(rm pmetric.ResourceMetrics) int {
	total := 0
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		total += rm.ScopeMetrics().At(i).Metrics().Len()
	}
	return total
}

func snapshot(t *testing.T, rm pmetric.ResourceMetrics) string {
	t.Helper()
	one := pmetric.NewMetrics()
	rm.CopyTo(one.ResourceMetrics().AppendEmpty())
	data, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(one)
	require.NoError(t, err)
	return string(data)
}

func gaugePoint(t *testing.T, m pmetric.Metric, key, value string) pmetric.NumberDataPoint {
	t.Helper()
	require.Equal(t, pmetric.MetricTypeGauge, m.Type())
	dps := m.Gauge().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		if value == "" && dps.At(i).Attributes().Len() == 0 {
			return dps.At(i)
		}
		if v, ok := dps.At(i).Attributes().Get(key); ok && v.Str() == value {
			return dps.At(i)
		}
	}
	require.Failf(t, "data point not found", "%s=%s", key, value)
	return pmetric.NumberDataPoint{}
}

func TestConsumeMetricsCPUUtilizationIsCPUTimeOverUptime(t *testing.T) {
	in := scrape(proc{name: "nginx", pid: 10, uptime: seconds(100), cpu: map[string]float64{"user": 3, "system": 2}})

	out := run(t, in)

	m, _, ok := findMetric(out.ResourceMetrics().At(0), "process.cpu.lifetime.utilization")
	require.True(t, ok)
	assert.Equal(t, "1", m.Unit())
	require.Equal(t, 1, m.Gauge().DataPoints().Len())
	dp := gaugePoint(t, m, "", "")
	assert.InDelta(t, 0.05, dp.DoubleValue(), 1e-12)
	assert.Equal(t, ts(time.Second), dp.Timestamp(), "the timestamp of the cpu points")
}

func TestConsumeMetricsIORateIsBytesOverUptimePerDirection(t *testing.T) {
	in := scrape(proc{name: "postgres", pid: 11, uptime: seconds(100), io: map[string]int64{"read": 1000, "write": 500}})

	out := run(t, in)

	m, _, ok := findMetric(out.ResourceMetrics().At(0), "process.disk.io.lifetime.rate")
	require.True(t, ok)
	assert.Equal(t, "By/s", m.Unit())
	require.Equal(t, 2, m.Gauge().DataPoints().Len())
	assert.InDelta(t, 10.0, gaugePoint(t, m, "direction", "read").DoubleValue(), 1e-12)
	assert.InDelta(t, 5.0, gaugePoint(t, m, "direction", "write").DoubleValue(), 1e-12)
	assert.Equal(t, ts(2*time.Second), gaugePoint(t, m, "direction", "read").Timestamp())
}

func TestConsumeMetricsSourceMetricsAreUnchangedAndTheNewOnesComeLast(t *testing.T) {
	in := scrape(proc{name: "nginx", pid: 10, uptime: seconds(100), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 7}})
	want := map[string]string{}
	for _, name := range []string{"process.uptime", "process.cpu.time", "process.disk.io"} {
		m, _, _ := findMetric(in.ResourceMetrics().At(0), name)
		one := pmetric.NewMetrics()
		m.CopyTo(one.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty())
		data, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(one)
		require.NoError(t, err)
		want[name] = string(data)
	}

	out := run(t, in)

	got := map[string]string{}
	rm := out.ResourceMetrics().At(0)
	ms := rm.ScopeMetrics().At(0).Metrics()
	require.Equal(t, 5, ms.Len())
	for i := 0; i < 3; i++ {
		one := pmetric.NewMetrics()
		ms.At(i).CopyTo(one.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty())
		data, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(one)
		require.NoError(t, err)
		got[ms.At(i).Name()] = string(data)
	}
	assert.Equal(t, want, got)
	assert.Equal(t, "process.cpu.lifetime.utilization", ms.At(3).Name())
	assert.Equal(t, "process.disk.io.lifetime.rate", ms.At(4).Name())
}

func TestConsumeMetricsResourceWithoutUsableUptimeIsUnchanged(t *testing.T) {
	tests := map[string]proc{
		"no uptime metric": {name: "a", pid: 1, cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 1}},
		"zero uptime":      {name: "b", pid: 2, uptime: seconds(0), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 1}},
		"negative uptime":  {name: "c", pid: 3, uptime: seconds(-5), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 1}},
	}
	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			in := scrape(p)
			want := snapshot(t, in.ResourceMetrics().At(0))

			out := run(t, in)

			assert.Equal(t, want, snapshot(t, out.ResourceMetrics().At(0)))
		})
	}
}

func TestConsumeMetricsOnlyTheMetricsThatExistGetARatio(t *testing.T) {
	in := scrape(
		proc{name: "cpuonly", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}},
		proc{name: "ioonly", pid: 2, uptime: seconds(10), io: map[string]int64{"read": 10}},
	)

	out := run(t, in)

	cpuOnly, ioOnly := out.ResourceMetrics().At(0), out.ResourceMetrics().At(1)
	_, _, hasCPU := findMetric(cpuOnly, "process.cpu.lifetime.utilization")
	_, _, hasIO := findMetric(cpuOnly, "process.disk.io.lifetime.rate")
	assert.True(t, hasCPU)
	assert.False(t, hasIO)
	_, _, hasCPU = findMetric(ioOnly, "process.cpu.lifetime.utilization")
	_, _, hasIO = findMetric(ioOnly, "process.disk.io.lifetime.rate")
	assert.False(t, hasCPU)
	assert.True(t, hasIO)
}

func TestConsumeMetricsDeltaSumsAreSkipped(t *testing.T) {
	in := scrape(proc{name: "a", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 1}})
	for _, name := range []string{"process.cpu.time", "process.disk.io"} {
		m, _, _ := findMetric(in.ResourceMetrics().At(0), name)
		m.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	}

	out := run(t, in)

	assert.Equal(t, 3, metricCount(out.ResourceMetrics().At(0)))
}

func TestConsumeMetricsIntegerUptimeIsRead(t *testing.T) {
	in := scrape(proc{name: "a", pid: 1, uptime: seconds(0), cpu: map[string]float64{"user": 5}})
	uptime, _, _ := findMetric(in.ResourceMetrics().At(0), "process.uptime")
	uptime.Gauge().DataPoints().At(0).SetIntValue(50)

	out := run(t, in)

	m, _, ok := findMetric(out.ResourceMetrics().At(0), "process.cpu.lifetime.utilization")
	require.True(t, ok)
	assert.InDelta(t, 0.1, gaugePoint(t, m, "", "").DoubleValue(), 1e-12)
}

func TestConsumeMetricsUptimeThatIsNotAGaugeIsIgnored(t *testing.T) {
	in := scrape(proc{name: "a", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}})
	uptime, _, _ := findMetric(in.ResourceMetrics().At(0), "process.uptime")
	uptime.SetEmptySum().DataPoints().AppendEmpty().SetDoubleValue(10)

	out := run(t, in)

	assert.Equal(t, 2, metricCount(out.ResourceMetrics().At(0)))
}

func TestConsumeMetricsEachProcessUsesItsOwnUptime(t *testing.T) {
	in := scrape(
		proc{name: "young", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}},
		proc{name: "old", pid: 2, uptime: seconds(1000), cpu: map[string]float64{"user": 1}},
	)

	out := run(t, in)

	young, _, _ := findMetric(out.ResourceMetrics().At(0), "process.cpu.lifetime.utilization")
	old, _, _ := findMetric(out.ResourceMetrics().At(1), "process.cpu.lifetime.utilization")
	assert.InDelta(t, 0.1, gaugePoint(t, young, "", "").DoubleValue(), 1e-12)
	assert.InDelta(t, 0.001, gaugePoint(t, old, "", "").DoubleValue(), 1e-12)
}

func TestConsumeMetricsRatioLandsInTheScopeOfItsSource(t *testing.T) {
	in := scrape(proc{name: "a", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 10}})
	rm := in.ResourceMetrics().At(0)
	other := rm.ScopeMetrics().AppendEmpty()
	other.Scope().SetName("other")
	cpu, sm, _ := findMetric(rm, "process.cpu.time")
	cpu.CopyTo(other.Metrics().AppendEmpty())
	sm.Metrics().RemoveIf(func(m pmetric.Metric) bool { return m.Name() == "process.cpu.time" })

	out := run(t, in)

	_, cpuScope, _ := findMetric(out.ResourceMetrics().At(0), "process.cpu.lifetime.utilization")
	_, ioScope, _ := findMetric(out.ResourceMetrics().At(0), "process.disk.io.lifetime.rate")
	assert.Equal(t, "other", cpuScope.Scope().Name())
	assert.Equal(t, "hostmetrics", ioScope.Scope().Name())
}

func TestConsumeMetricsSecondPassChangesNothing(t *testing.T) {
	in := scrape(proc{name: "a", pid: 1, uptime: seconds(10), cpu: map[string]float64{"user": 1}, io: map[string]int64{"read": 10}})
	once := run(t, in)
	want := snapshot(t, once.ResourceMetrics().At(0))

	twice := run(t, once)

	assert.Equal(t, want, snapshot(t, twice.ResourceMetrics().At(0)))
	assert.Equal(t, 5, metricCount(twice.ResourceMetrics().At(0)))
}

func TestConsumeMetricsEmptyBatchPassesThrough(t *testing.T) {
	out := run(t, pmetric.NewMetrics())

	assert.Equal(t, 0, out.ResourceMetrics().Len())
}

func TestConsumeMetricsReturnsTheNextConsumersErrorAsIs(t *testing.T) {
	boom := errors.New("downstream refused")
	p := newProcessor(consumertest.NewErr(boom))

	got := p.ConsumeMetrics(context.Background(), scrape(proc{name: "a", pid: 1}))

	assert.Same(t, boom, got)
}

func TestProcessorLifecycleAndCapabilities(t *testing.T) {
	p := newProcessor(new(consumertest.MetricsSink))

	assert.NoError(t, p.Start(context.Background(), nil))
	assert.NoError(t, p.Shutdown(context.Background()))
	assert.True(t, p.Capabilities().MutatesData)
}

func BenchmarkConsumeMetrics(b *testing.B) {
	template := make([]proc, 0, 200)
	for i := 0; i < 200; i++ {
		template = append(template, proc{
			name: fmt.Sprintf("proc%d", i), pid: int64(i), uptime: seconds(float64(100 + i)),
			cpu: map[string]float64{"user": 1.5, "system": 0.5, "wait": 0.1},
			io:  map[string]int64{"read": 1000, "write": 500},
		})
	}
	p := newProcessor(consumertest.NewNop())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		md := scrape(template...)
		b.StartTimer()
		if err := p.ConsumeMetrics(context.Background(), md); err != nil {
			b.Fatal(err)
		}
	}
}
