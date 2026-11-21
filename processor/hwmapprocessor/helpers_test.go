// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor milli

package hwmapprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// pt is one input point: a value and attributes given as key, value pairs.
type pt struct {
	value       float64
	isInt       bool
	attrs       map[string]string
	ts, startTS int64
}

func ip(v int64, kv ...string) pt { return pt{value: float64(v), isInt: true, attrs: pairs(kv)} }
func dp(v float64, kv ...string) pt {
	return pt{value: v, attrs: pairs(kv)}
}

func pairs(kv []string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// spec is one input metric.
type spec struct {
	name, unit string
	sum        bool
	delta      bool
	monotonic  bool
	pts        []pt
}

func gaugeOf(name, unit string, pts ...pt) spec { return spec{name: name, unit: unit, pts: pts} }

func build(res map[string]string, specs ...spec) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	for k, v := range res {
		rm.Resource().Attributes().PutStr(k, v)
	}
	sm := rm.ScopeMetrics().AppendEmpty()
	for _, s := range specs {
		m := sm.Metrics().AppendEmpty()
		m.SetName(s.name)
		m.SetUnit(s.unit)
		var dps pmetric.NumberDataPointSlice
		if s.sum {
			sum := m.SetEmptySum()
			sum.SetIsMonotonic(s.monotonic)
			if s.delta {
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
			} else {
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
			}
			dps = sum.DataPoints()
		} else {
			dps = m.SetEmptyGauge().DataPoints()
		}
		for _, p := range s.pts {
			d := dps.AppendEmpty()
			if p.isInt {
				d.SetIntValue(int64(p.value))
			} else {
				d.SetDoubleValue(p.value)
			}
			for k, v := range p.attrs {
				d.Attributes().PutStr(k, v)
			}
			d.SetTimestamp(pcommon.Timestamp(p.ts))
			d.SetStartTimestamp(pcommon.Timestamp(p.startTS))
		}
	}
	return md
}

// flat is one output point with its metric.
type flat struct {
	metric, unit string
	kind         pmetric.MetricType
	temporality  pmetric.AggregationTemporality
	monotonic    bool
	value        float64
	isInt        bool
	attrs        map[string]string
	res          map[string]string
	ts, startTS  int64
}

func flatten(md pmetric.Metrics) []flat {
	var out []flat
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		res := map[string]string{}
		rms.At(i).Resource().Attributes().Range(func(k string, v pcommon.Value) bool { res[k] = v.AsString(); return true })
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				var dps pmetric.NumberDataPointSlice
				f := flat{metric: m.Name(), unit: m.Unit(), kind: m.Type(), res: res}
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					dps = m.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					dps = m.Sum().DataPoints()
					f.temporality = m.Sum().AggregationTemporality()
					f.monotonic = m.Sum().IsMonotonic()
				default:
					continue
				}
				for n := 0; n < dps.Len(); n++ {
					d := dps.At(n)
					p := f
					p.attrs = map[string]string{}
					d.Attributes().Range(func(k string, v pcommon.Value) bool { p.attrs[k] = v.AsString(); return true })
					p.isInt = d.ValueType() == pmetric.NumberDataPointValueTypeInt
					if p.isInt {
						p.value = float64(d.IntValue())
					} else {
						p.value = d.DoubleValue()
					}
					p.ts, p.startTS = int64(d.Timestamp()), int64(d.StartTimestamp())
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func named(points []flat, name string) []flat {
	var out []flat
	for _, p := range points {
		if p.metric == name {
			out = append(out, p)
		}
	}
	return out
}

// harness runs one config over data and keeps the logs and counters for the test.
type harness struct {
	t      *testing.T
	e      *engine
	logs   *observer.ObservedLogs
	reader *metric.ManualReader
}

func newHarness(t *testing.T, cfg *Config) *harness {
	t.Helper()
	require.NoError(t, cfg.Validate())
	reader := metric.NewManualReader()
	core, logs := observer.New(zapcore.DebugLevel)
	ts := component.TelemetrySettings{Logger: zap.New(core), MeterProvider: metric.NewMeterProvider(metric.WithReader(reader))}
	e, err := newEngine(cfg, ts)
	require.NoError(t, err)
	return &harness{t: t, e: e, logs: logs, reader: reader}
}

func (h *harness) run(md pmetric.Metrics) []flat {
	h.e.process(context.Background(), md)
	return flatten(md)
}

// counter sums an instrument over the points whose attribute key has the value; an empty key sums all.
func (h *harness) counter(name, key, value string) int64 {
	h.t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(h.t, h.reader.Collect(context.Background(), &rm))
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(h.t, ok, "%s is not an int sum", name)
			for _, p := range sum.DataPoints {
				if key == "" {
					total += p.Value
					continue
				}
				if v, ok := p.Attributes.Value(attribute.Key(key)); ok && v.AsString() == value {
					total += p.Value
				}
			}
		}
	}
	return total
}
