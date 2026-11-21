// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"path"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/hwmonreceiver"

type scraper struct {
	logger  *zap.Logger
	src     source
	opts    readOptions
	include []string
	exclude []string

	noSensorsLogged bool
	errorLogged     bool
}

func newScraper(logger *zap.Logger, cfg *Config, src source) *scraper {
	return &scraper{
		logger:  logger,
		src:     src,
		opts:    cfg.options(),
		include: cfg.Include,
		exclude: cfg.Exclude,
	}
}

// scrape reads the sensors once. A host with no sensor gives one info line
// until sensors appear; another error gives one warning per episode.
func (s *scraper) scrape(now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	readings, err := s.src.read(s.opts)
	switch {
	case errors.Is(err, errNoSensors):
		if !s.noSensorsLogged {
			s.logger.Info("No hardware sensors found; nothing is sent until some appear")
			s.noSensorsLogged = true
		}
		s.errorLogged = false
		return md
	case err != nil:
		if !s.errorLogged {
			s.logger.Warn("Failed to read the hardware sensors", zap.Error(err))
			s.errorLogged = true
		}
		return md
	}
	s.noSensorsLogged, s.errorLogged = false, false

	b := newBuilder(s.logger, md, pcommon.NewTimestampFromTime(now))
	for _, r := range readings {
		if s.selected(r) {
			b.add(r)
		}
	}
	return md
}

// selected applies include and exclude to "chip:label"; exclude wins.
func (s *scraper) selected(r reading) bool {
	key := r.parent + ":" + r.name
	if len(s.include) > 0 && !matchAny(s.include, key) {
		return false
	}
	return !matchAny(s.exclude, key)
}

func matchAny(globs []string, key string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, key); ok {
			return true
		}
	}
	return false
}

// builder adds each metric to the scope the first time a point needs it.
type builder struct {
	logger  *zap.Logger
	sm      pmetric.ScopeMetrics
	ts      pcommon.Timestamp
	metrics map[string]pmetric.Gauge
	noType  map[kind]bool
}

func newBuilder(logger *zap.Logger, md pmetric.Metrics, ts pcommon.Timestamp) *builder {
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	return &builder{logger: logger, sm: sm, ts: ts, metrics: map[string]pmetric.Gauge{}, noType: map[kind]bool{}}
}

// hwType returns the hw.type of the sensor kind. A kind with no value logs one debug line and
// gives no type.
func (b *builder) hwType(k kind) (string, bool) {
	v, ok := hwTypeOf(k)
	if !ok && !b.noType[k] {
		b.noType[k] = true
		b.logger.Debug("The sensor kind has no hw.type value; its metrics go without it", zap.Int("kind", int(k)))
	}
	return v, ok
}

func (b *builder) gauge(name, unit string) pmetric.Gauge {
	if g, ok := b.metrics[name]; ok {
		return g
	}
	m := b.sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	g := m.SetEmptyGauge()
	b.metrics[name] = g
	return g
}

func (b *builder) point(g pmetric.Gauge, r reading) pmetric.NumberDataPoint {
	dp := g.DataPoints().AppendEmpty()
	dp.SetTimestamp(b.ts)
	dp.Attributes().PutStr("hw.id", r.id)
	dp.Attributes().PutStr("hw.name", r.name)
	dp.Attributes().PutStr("hw.parent", r.parent)
	if t, ok := b.hwType(r.kind); ok {
		dp.Attributes().PutStr("hw.type", t)
	}
	return dp
}

func (b *builder) add(r reading) {
	switch r.kind {
	case kindTemperature:
		b.point(b.gauge("hw.temperature", "Cel"), r).SetDoubleValue(r.value)
		for _, l := range r.limits {
			dp := b.point(b.gauge("hw.temperature.limit", "Cel"), r)
			dp.Attributes().PutStr("hw.limit_type", l.limitType)
			dp.SetDoubleValue(l.celsius)
		}
	case kindFan:
		b.point(b.gauge("hw.fan.speed", "rpm"), r).SetIntValue(int64(r.value))
	case kindVoltage:
		b.point(b.gauge("hw.voltage", "V"), r).SetDoubleValue(r.value)
	}
}
