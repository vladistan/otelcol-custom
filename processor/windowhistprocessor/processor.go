// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// Reasons a sample is discarded.
const (
	reasonInvalid = "invalid"
	reasonLate    = "late"
)

// windowProcessor buffers the samples of grouped metrics and emits one point per
// series when each send window closes. Other metrics pass through at once.
type windowProcessor struct {
	next consumer.Metrics
	now  func() time.Time
	warn func(string)

	// routes maps a metric name to its group; fixed after construction.
	routes map[string]groupRoute

	mu       sync.Mutex
	windows  map[time.Duration]*window
	series   map[string]*series
	discards map[string]uint64

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ consumer.Metrics = (*windowProcessor)(nil)

func newWindowProcessor(cfg *Config, next consumer.Metrics) (*windowProcessor, error) {
	names := make([]string, 0, len(cfg.Groups))
	for name := range cfg.Groups {
		names = append(names, name)
	}
	sort.Strings(names)

	routes := map[string]groupRoute{}
	for _, name := range names {
		s, err := cfg.Resolve(name)
		if err != nil {
			return nil, err
		}
		if s.Mode == ModePlain && s.SendInterval == s.ScrapeInterval {
			continue // scrape = send: the group's data passes through unchanged
		}
		for _, metric := range s.Metrics {
			routes[metric] = groupRoute{name: name, GroupSettings: s}
		}
	}
	return &windowProcessor{
		next:     next,
		now:      time.Now,
		warn:     func(string) {},
		routes:   routes,
		windows:  map[time.Duration]*window{},
		series:   map[string]*series{},
		discards: map[string]uint64{},
	}, nil
}

func (p *windowProcessor) Start(ctx context.Context, _ component.Host) error {
	p.initWindows(p.now())
	// The tickers outlive Start's context, so they get their own.
	tickCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.cancel = cancel
	p.startTickers(tickCtx)
	return nil
}

func (p *windowProcessor) Shutdown(context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

func (p *windowProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *windowProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	buffered := false
	p.mu.Lock()
	md.ResourceMetrics().RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		rm.ScopeMetrics().RemoveIf(func(sm pmetric.ScopeMetrics) bool {
			sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
				if p.buffer(rm, sm, m) {
					buffered = true
					return true
				}
				return false
			})
			return sm.Metrics().Len() == 0
		})
		return rm.ScopeMetrics().Len() == 0
	})
	p.mu.Unlock()

	if buffered && md.ResourceMetrics().Len() == 0 {
		return nil
	}
	return p.next.ConsumeMetrics(ctx, md)
}

// buffer takes a grouped metric's samples into series state and reports whether
// it did. Metric shapes a group cannot summarize stay in the batch.
func (p *windowProcessor) buffer(rm pmetric.ResourceMetrics, sm pmetric.ScopeMetrics, m pmetric.Metric) bool {
	route, ok := p.routes[m.Name()]
	if !ok {
		return false
	}
	var points pmetric.NumberDataPointSlice
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		points = m.Gauge().DataPoints()
	case pmetric.MetricTypeSum:
		// Counter rates are built in a later step; until then a counter in a
		// histogram group goes on unchanged.
		if route.Mode == ModeHistogram && m.Sum().IsMonotonic() {
			return false
		}
		points = m.Sum().DataPoints()
	default:
		return false
	}
	for i := 0; i < points.Len(); i++ {
		p.addSample(route, rm, sm, m, points.At(i))
	}
	return true
}

func (p *windowProcessor) addSample(route groupRoute, rm pmetric.ResourceMetrics, sm pmetric.ScopeMetrics,
	m pmetric.Metric, dp pmetric.NumberDataPoint) {
	value := numberValue(dp)
	if route.Mode == ModeHistogram && (math.IsNaN(value) || math.IsInf(value, 0)) {
		p.discards[reasonInvalid]++
		return
	}
	w := p.windows[route.SendInterval]
	if w != nil && dp.Timestamp().AsTime().Before(w.start.Add(-route.SendInterval)) {
		p.discards[reasonLate]++
		return
	}

	key := seriesKey(resourceKey(rm.Resource()), scopeKey(sm.Scope()), m, dp.Attributes())
	s, ok := p.series[key]
	if !ok {
		s = newSeries(route, rm, sm, m, dp.Attributes(), p.now())
		p.series[key] = s
	}
	s.samples++
	if route.Mode == ModePlain {
		if s.samples == 1 || dp.Timestamp() >= s.last.Timestamp() {
			dp.CopyTo(s.last)
		}
		return
	}
	s.window.Update(value)
}

func (p *windowProcessor) discarded(reason string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.discards[reason]
}
