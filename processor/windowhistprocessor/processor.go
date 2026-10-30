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
	"go.opentelemetry.io/otel/metric"
)

// windowProcessor buffers the samples of grouped metrics and emits one point per
// series when each send window closes. Other metrics pass through at once.
type windowProcessor struct {
	next consumer.Metrics
	now  func() time.Time
	warn func(string)

	// routes maps a metric name to its group; fixed after construction.
	routes map[string]groupRoute

	tel *telemetry

	mu            sync.Mutex
	windows       map[time.Duration]*window
	series        map[string]*series
	spacingWarned map[string]bool

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ consumer.Metrics = (*windowProcessor)(nil)

func newWindowProcessor(cfg *Config, next consumer.Metrics, meter metric.Meter) (*windowProcessor, error) {
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
	p := &windowProcessor{
		next:          next,
		now:           time.Now,
		warn:          func(string) {},
		routes:        routes,
		windows:       map[time.Duration]*window{},
		series:        map[string]*series{},
		spacingWarned: map[string]bool{},
	}
	tel, err := newTelemetry(meter, p.activeSeries)
	if err != nil {
		return nil, err
	}
	p.tel = tel
	return p, nil
}

func (p *windowProcessor) Start(ctx context.Context, _ component.Host) error {
	p.initWindows(p.now())
	// The tickers outlive Start's context, so they get their own.
	tickCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.cancel = cancel
	p.startTickers(tickCtx)
	return nil
}

func (p *windowProcessor) Shutdown(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return p.flushAll(ctx)
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
		// Only a cumulative counter can be turned into a rate.
		if route.Mode == ModeHistogram && m.Sum().IsMonotonic() &&
			m.Sum().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
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
	p.tel.receive()
	value := numberValue(dp)
	if route.Mode == ModeHistogram && (math.IsNaN(value) || math.IsInf(value, 0)) {
		p.tel.discard(reasonInvalid)
		return
	}
	at := dp.Timestamp().AsTime()
	if w := p.windows[route.SendInterval]; w != nil && at.Before(w.start.Add(-route.SendInterval)) {
		p.tel.discard(reasonLate)
		return
	}

	key := seriesKey(resourceKey(rm.Resource()), scopeKey(sm.Scope()), m, dp.Attributes())
	s, ok := p.series[key]
	if !ok {
		s = newSeries(route, rm, sm, m, dp.Attributes(), p.now())
		p.series[key] = s
	}
	p.checkSpacing(s, at)
	s.samples++
	switch {
	case route.Mode == ModePlain:
		if s.samples == 1 || dp.Timestamp() >= s.last.Timestamp() {
			dp.CopyTo(s.last)
		}
	case s.counter:
		p.addCounter(s, dp, value, at)
	default:
		s.window.Update(value)
	}
}
