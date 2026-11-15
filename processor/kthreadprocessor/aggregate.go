// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// perProcessKeys name one process, so an aggregate of many never carries them.
var perProcessKeys = []string{
	"process.pid",
	"process.parent_pid",
	"process.command",
	"process.command_line",
	"process.executable.path",
}

// aggregate collects the merged resources in plain Go values: the source
// resources are removed from the batch while it is being read.
type aggregate struct {
	scrapeEnd map[pcommon.Timestamp]pcommon.Timestamp
	merged    int
	common    map[string]pcommon.Value
	schema    string
	scopes    []*scopeAgg
}

// uptimeMetric is not summed: a total of ages has no meaning and would skew
// ratios computed after the merge.
const uptimeMetric = "process.uptime"

type scopeAgg struct {
	key     string
	name    string
	version string
	schema  string
	metrics []*metricAgg
}

type metricAgg struct {
	key         string
	name        string
	description string
	unit        string
	kind        pmetric.MetricType
	temporality pmetric.AggregationTemporality
	monotonic   bool
	points      []*pointAgg
}

type pointAgg struct {
	key        string
	attrs      pcommon.Map
	intSum     int64
	doubleSum  float64
	sawDouble  bool
	start, end pcommon.Timestamp
}

// newAggregate takes the scrape each point timestamp belongs to, see scrapeEnds.
func newAggregate(scrapeEnd map[pcommon.Timestamp]pcommon.Timestamp) *aggregate {
	return &aggregate{scrapeEnd: scrapeEnd}
}

func (a *aggregate) add(rm pmetric.ResourceMetrics) {
	a.mergeResource(rm.Resource().Attributes())
	if a.merged == 0 {
		a.schema = rm.SchemaUrl()
	} else if a.schema != rm.SchemaUrl() {
		a.schema = ""
	}
	a.merged++
	for i := 0; i < rm.ScopeMetrics().Len(); i++ {
		sm := rm.ScopeMetrics().At(i)
		scope := a.scopeFor(sm)
		for j := 0; j < sm.Metrics().Len(); j++ {
			scope.addMetric(sm.Metrics().At(j), a.scrapeEnd)
		}
	}
}

// mergeResource keeps the attributes every merged resource agrees on.
func (a *aggregate) mergeResource(attrs pcommon.Map) {
	if a.merged == 0 {
		a.common = map[string]pcommon.Value{}
		attrs.Range(func(k string, v pcommon.Value) bool {
			if !slices.Contains(perProcessKeys, k) && k != executableNameAttr {
				copied := pcommon.NewValueEmpty()
				v.CopyTo(copied)
				a.common[k] = copied
			}
			return true
		})
		return
	}
	for k, kept := range a.common {
		if v, ok := attrs.Get(k); !ok || !v.Equal(kept) {
			delete(a.common, k)
		}
	}
}

func (a *aggregate) scopeFor(sm pmetric.ScopeMetrics) *scopeAgg {
	scope := sm.Scope()
	key := scope.Name() + "\x00" + scope.Version() + "\x00" + sm.SchemaUrl()
	for _, s := range a.scopes {
		if s.key == key {
			return s
		}
	}
	s := &scopeAgg{key: key, name: scope.Name(), version: scope.Version(), schema: sm.SchemaUrl()}
	a.scopes = append(a.scopes, s)
	return s
}

// addMetric sums sums and gauges; other metric types have no sum and are left out.
// A metric whose points are all stale markers adds nothing.
func (s *scopeAgg) addMetric(m pmetric.Metric, scrapeEnd map[pcommon.Timestamp]pcommon.Timestamp) {
	if m.Name() == uptimeMetric {
		return
	}
	var dps pmetric.NumberDataPointSlice
	agg := &metricAgg{name: m.Name(), description: m.Description(), unit: m.Unit(), kind: m.Type()}
	switch m.Type() {
	case pmetric.MetricTypeSum:
		dps = m.Sum().DataPoints()
		agg.temporality = m.Sum().AggregationTemporality()
		agg.monotonic = m.Sum().IsMonotonic()
	case pmetric.MetricTypeGauge:
		dps = m.Gauge().DataPoints()
	default:
		return
	}
	agg.key = strings.Join([]string{m.Name(), m.Type().String(), agg.temporality.String(), m.Unit()}, "\x00")
	var target *metricAgg
	for i := 0; i < dps.Len(); i++ {
		if dps.At(i).Flags().NoRecordedValue() {
			continue
		}
		if target == nil {
			target = s.metricFor(agg)
		}
		target.addPoint(dps.At(i), scrapeEnd[dps.At(i).Timestamp()])
	}
}

func (s *scopeAgg) metricFor(m *metricAgg) *metricAgg {
	for _, existing := range s.metrics {
		if existing.key == m.key {
			return existing
		}
	}
	s.metrics = append(s.metrics, m)
	return m
}

// addPoint sums points that share attributes and scrape (end is the latest time
// of that scrape), so two scrapes in one batch stay two points.
func (m *metricAgg) addPoint(dp pmetric.NumberDataPoint, end pcommon.Timestamp) {
	key := attributesKey(dp.Attributes()) + "\x00@" + strconv.FormatUint(uint64(end), 10)
	var pt *pointAgg
	for _, existing := range m.points {
		if existing.key == key {
			pt = existing
			break
		}
	}
	if pt == nil {
		pt = &pointAgg{key: key, attrs: pcommon.NewMap()}
		dp.Attributes().CopyTo(pt.attrs)
		m.points = append(m.points, pt)
	}
	switch dp.ValueType() {
	case pmetric.NumberDataPointValueTypeInt:
		pt.intSum += dp.IntValue()
	case pmetric.NumberDataPointValueTypeDouble:
		pt.doubleSum += dp.DoubleValue()
		pt.sawDouble = true
	}
	if start := dp.StartTimestamp(); start != 0 && (pt.start == 0 || start < pt.start) {
		pt.start = start
	}
	pt.end = max(pt.end, end)
}

// attributesKey is the same for two attribute sets with the same pairs in any order.
func attributesKey(attrs pcommon.Map) string {
	pairs := make([]string, 0, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		pairs = append(pairs, k+"="+v.AsString())
		return true
	})
	slices.Sort(pairs)
	return strings.Join(pairs, "\x00")
}

// appendTo adds the aggregate as the last resource; nothing is added when no
// resource was merged or none carried a sum or gauge.
func (a *aggregate) appendTo(md pmetric.Metrics, name string) {
	if a.merged == 0 || !a.hasMetrics() {
		return
	}
	rm := md.ResourceMetrics().AppendEmpty()
	rm.SetSchemaUrl(a.schema)
	attrs := rm.Resource().Attributes()
	for _, k := range slices.Sorted(maps.Keys(a.common)) {
		a.common[k].CopyTo(attrs.PutEmpty(k))
	}
	attrs.PutStr(executableNameAttr, name)
	for _, s := range a.scopes {
		if len(s.metrics) == 0 {
			continue
		}
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName(s.name)
		sm.Scope().SetVersion(s.version)
		sm.SetSchemaUrl(s.schema)
		for _, m := range s.metrics {
			m.appendTo(sm.Metrics().AppendEmpty())
		}
	}
}

func (a *aggregate) hasMetrics() bool {
	for _, s := range a.scopes {
		if len(s.metrics) > 0 {
			return true
		}
	}
	return false
}

// appendTo writes the metric. A cumulative monotonic sum is written
// non-monotonic: the per-scrape total falls when a thread exits.
func (m *metricAgg) appendTo(out pmetric.Metric) {
	out.SetName(m.name)
	out.SetDescription(m.description)
	out.SetUnit(m.unit)
	var dps pmetric.NumberDataPointSlice
	if m.kind == pmetric.MetricTypeSum {
		sum := out.SetEmptySum()
		sum.SetAggregationTemporality(m.temporality)
		sum.SetIsMonotonic(m.monotonic && m.temporality != pmetric.AggregationTemporalityCumulative)
		dps = sum.DataPoints()
	} else {
		dps = out.SetEmptyGauge().DataPoints()
	}
	for _, pt := range m.points {
		dp := dps.AppendEmpty()
		pt.attrs.CopyTo(dp.Attributes())
		dp.SetStartTimestamp(pt.start)
		dp.SetTimestamp(pt.end)
		if pt.sawDouble {
			dp.SetDoubleValue(pt.doubleSum + float64(pt.intSum))
		} else {
			dp.SetIntValue(pt.intSum)
		}
	}
}
