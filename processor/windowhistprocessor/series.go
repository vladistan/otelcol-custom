// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"fmt"
	"time"

	"github.com/lightstep/go-expohisto/structure"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// groupRoute is the resolved group a metric name belongs to.
type groupRoute struct {
	name string
	GroupSettings
}

// series is the buffered state of one metric stream.
type series struct {
	route    groupRoute
	resource pcommon.Resource
	resKey   string
	scope    pcommon.InstrumentationScope
	scopeKey string
	name     string
	unit     string
	desc     string
	attrs    pcommon.Map

	firstSeen time.Time
	samples   int

	window *structure.Histogram[float64]
	cum    *structure.Histogram[float64] // cumulative temporality only

	last        pmetric.NumberDataPoint // plain groups: newest data point in the window
	lastKind    pmetric.MetricType
	monotonic   bool
	temporality pmetric.AggregationTemporality
}

func resourceKey(r pcommon.Resource) string {
	return fmt.Sprintf("%v", r.Attributes().AsRaw())
}

func scopeKey(s pcommon.InstrumentationScope) string {
	return fmt.Sprintf("%s/%s", s.Name(), s.Version())
}

func seriesKey(resKey, scKey string, m pmetric.Metric, attrs pcommon.Map) string {
	return fmt.Sprintf("%s|%s|%s|%v", m.Name(), resKey, scKey, attrs.AsRaw())
}

func newSeries(route groupRoute, rm pmetric.ResourceMetrics, sm pmetric.ScopeMetrics, m pmetric.Metric,
	attrs pcommon.Map, now time.Time) *series {
	s := &series{
		route:     route,
		resource:  pcommon.NewResource(),
		scope:     pcommon.NewInstrumentationScope(),
		name:      m.Name(),
		unit:      m.Unit(),
		desc:      m.Description(),
		attrs:     pcommon.NewMap(),
		firstSeen: now,
		last:      pmetric.NewNumberDataPoint(),
		lastKind:  m.Type(),
	}
	rm.Resource().CopyTo(s.resource)
	sm.Scope().CopyTo(s.scope)
	attrs.CopyTo(s.attrs)
	s.resKey = resourceKey(s.resource)
	s.scopeKey = scopeKey(s.scope)
	if m.Type() == pmetric.MetricTypeSum {
		s.monotonic = m.Sum().IsMonotonic()
		s.temporality = m.Sum().AggregationTemporality()
	}
	if route.Mode == ModeHistogram {
		cfg := structure.NewConfig(structure.WithMaxSize(int32(route.MaxSize)))
		s.window = new(structure.Histogram[float64])
		s.window.Init(cfg)
		if route.Temporality == TemporalityCumulative {
			s.cum = new(structure.Histogram[float64])
			s.cum.Init(cfg)
		}
	}
	return s
}

func numberValue(dp pmetric.NumberDataPoint) float64 {
	if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
		return float64(dp.IntValue())
	}
	return dp.DoubleValue()
}
