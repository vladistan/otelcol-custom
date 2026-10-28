// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lightstep/go-expohisto/structure"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// window is the open window of one send interval.
type window struct {
	start time.Time
}

// initWindows opens one window per distinct send interval, starting at now.
func (p *windowProcessor) initWindows(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, route := range p.routes {
		if p.windows[route.SendInterval] == nil {
			p.windows[route.SendInterval] = &window{start: now}
		}
	}
}

// flush emits one point per series of the given send interval that has samples,
// closes the window at end, and opens the next one.
func (p *windowProcessor) flush(ctx context.Context, send time.Duration, end time.Time) error {
	out, ok := p.collect(send, end)
	if !ok {
		return nil
	}
	return p.next.ConsumeMetrics(ctx, out)
}

func (p *windowProcessor) collect(send time.Duration, end time.Time) (pmetric.Metrics, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	w := p.windows[send]
	if w == nil {
		return pmetric.Metrics{}, false
	}
	keys := make([]string, 0, len(p.series))
	for key, s := range p.series {
		if s.route.SendInterval == send && s.samples > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	out := pmetric.NewMetrics()
	resources := map[string]pmetric.ResourceMetrics{}
	scopes := map[string]pmetric.ScopeMetrics{}
	for _, key := range keys {
		s := p.series[key]
		rm, ok := resources[s.resKey]
		if !ok {
			rm = out.ResourceMetrics().AppendEmpty()
			s.resource.CopyTo(rm.Resource())
			resources[s.resKey] = rm
		}
		scopeID := s.resKey + "\x00" + s.scopeKey
		sm, ok := scopes[scopeID]
		if !ok {
			sm = rm.ScopeMetrics().AppendEmpty()
			s.scope.CopyTo(sm.Scope())
			scopes[scopeID] = sm
		}
		emit(sm.Metrics().AppendEmpty(), s, w.start, end)
		s.endWindow()
	}
	w.start = end
	return out, len(keys) > 0
}

func emit(m pmetric.Metric, s *series, start, end time.Time) {
	m.SetName(s.name)
	m.SetUnit(s.unit)
	m.SetDescription(s.desc)
	if s.route.Mode == ModePlain {
		if s.lastKind == pmetric.MetricTypeGauge {
			s.last.CopyTo(m.SetEmptyGauge().DataPoints().AppendEmpty())
			return
		}
		sum := m.SetEmptySum()
		sum.SetIsMonotonic(s.monotonic)
		sum.SetAggregationTemporality(s.temporality)
		s.last.CopyTo(sum.DataPoints().AppendEmpty())
		return
	}

	eh := m.SetEmptyExponentialHistogram()
	h, from := s.window, start
	eh.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	if s.cum != nil {
		s.cum.MergeFrom(s.window)
		h, from = s.cum, s.firstSeen
		eh.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	}
	dp := eh.DataPoints().AppendEmpty()
	s.attrs.CopyTo(dp.Attributes())
	fillHistogramPoint(dp, h, from, end)
}

func fillHistogramPoint(dp pmetric.ExponentialHistogramDataPoint, h *structure.Histogram[float64], start, end time.Time) {
	dp.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
	dp.SetTimestamp(pcommon.NewTimestampFromTime(end))
	dp.SetCount(h.Count())
	dp.SetSum(h.Sum())
	dp.SetMin(h.Min())
	dp.SetMax(h.Max())
	dp.SetScale(h.Scale())
	dp.SetZeroCount(h.ZeroCount())
	fillBuckets(dp.Positive(), h.Positive())
	fillBuckets(dp.Negative(), h.Negative())
}

func fillBuckets(dst pmetric.ExponentialHistogramDataPointBuckets, src *structure.Buckets) {
	dst.SetOffset(src.Offset())
	counts := dst.BucketCounts()
	counts.EnsureCapacity(int(src.Len()))
	for i := uint32(0); i < src.Len(); i++ {
		counts.Append(src.At(i))
	}
}

// endWindow clears what the closed window held.
func (s *series) endWindow() {
	s.samples = 0
	if s.window != nil {
		s.window.Clear()
	}
}

// startTickers runs one ticker per send interval until ctx ends.
func (p *windowProcessor) startTickers(ctx context.Context) {
	p.mu.Lock()
	intervals := make([]time.Duration, 0, len(p.windows))
	for send := range p.windows {
		intervals = append(intervals, send)
	}
	p.mu.Unlock()

	for _, send := range intervals {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			ticker := time.NewTicker(send)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := p.flush(ctx, send, p.now()); err != nil {
						p.warn(fmt.Sprintf("windowhist: forwarding the %v window failed: %v", send, err))
					}
				}
			}
		}()
	}
}
