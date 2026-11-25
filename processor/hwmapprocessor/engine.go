// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	unmappedPointsName = "otelcol_hwmap_unmapped_points"
	unmappedStatesName = "otelcol_hwmap_unmapped_states"
	// maxTextLen bounds a text taken from a point before it becomes an attribute.
	maxTextLen = 256
	// maxLoggedValues bounds the table of state texts already logged.
	maxLoggedValues = 1024
)

type kv struct{ key, value string }

// rule is a Rule prepared for matching.
type rule struct {
	name      string
	target    string
	unit      string
	scale     float64
	scaled    bool
	dropValue *float64
	attrs     []kv
	status    *statusPlan
	enum      *enumPlan
	labels    []labelTemplate
	indexAttr string
	indexName map[string]string
	constants []kv
	resource  []kv
}

// labelTemplate sets the point attribute key from a template over the source attributes.
type labelTemplate struct {
	key string
	tpl template
}

// statusPlan is a Status prepared for use.
type statusPlan struct {
	attr     string // the attribute that holds the state text; empty reads the point value
	states   []string
	mapping  map[string]string
	def      string
	stateKey string
	rawKey   string
}

// enumPlan is an EnumGauge prepared for use.
type enumPlan struct {
	attr     string // the attribute that holds the state text; empty reads the point value
	on       map[string]bool
	names    map[string]string
	stateKey string
}

// engine applies the rules to metric data.
type engine struct {
	byMetric       map[string][]*rule
	logger         *zap.Logger
	unmappedPoints metric.Int64Counter
	unmappedStates metric.Int64Counter

	mu       sync.Mutex
	logged   map[string]struct{}
	capNoted bool
}

func newEngine(cfg *Config, ts component.TelemetrySettings) (*engine, error) {
	meter := ts.MeterProvider.Meter("hwmapprocessor")
	points, err := meter.Int64Counter(unmappedPointsName, metric.WithDescription("Points no hwmap rule matched, by metric name"))
	if err != nil {
		return nil, err
	}
	states, err := meter.Int64Counter(unmappedStatesName, metric.WithDescription("Points whose state text is not in the status map of the rule"))
	if err != nil {
		return nil, err
	}
	e := &engine{
		byMetric:       make(map[string][]*rule, len(cfg.Rules)),
		logger:         ts.Logger,
		unmappedPoints: points,
		unmappedStates: states,
		logged:         map[string]struct{}{},
	}
	for i := range cfg.Rules {
		r := compileRule(&cfg.Rules[i])
		e.byMetric[cfg.Rules[i].Match.Metric] = append(e.byMetric[cfg.Rules[i].Match.Metric], r)
	}
	return e, nil
}

func compileRule(c *Rule) *rule {
	r := &rule{name: c.Name, target: c.Target, unit: c.Unit, scale: 1, dropValue: c.DropWhenValue}
	if c.Scale != nil {
		r.scale, r.scaled = *c.Scale, true
	}
	for k, v := range c.Match.Attributes {
		r.attrs = append(r.attrs, kv{k, v})
	}
	sort.Slice(r.attrs, func(i, j int) bool { return r.attrs[i].key < r.attrs[j].key })
	for k, v := range c.Labels {
		tpl, _ := parseTemplate(v) // Validate has parsed it already
		r.labels = append(r.labels, labelTemplate{key: k, tpl: tpl})
	}
	sort.Slice(r.labels, func(i, j int) bool { return r.labels[i].key < r.labels[j].key })
	if c.IndexNames != nil {
		r.indexAttr, r.indexName = c.IndexNames.Attribute, c.IndexNames.Names
	}
	r.constants, r.resource = sortedPairs(c.Constants), sortedPairs(c.Resource)
	if s := c.Status; s != nil {
		r.status = &statusPlan{
			states: s.states(), mapping: s.Map, def: s.DefaultState,
			stateKey: s.stateAttribute(), rawKey: s.rawAttribute(),
		}
		if s.source() == sourceAttribute {
			r.status.attr = s.Attribute
		}
	}
	if g := c.EnumGauge; g != nil {
		r.enum = &enumPlan{on: make(map[string]bool, len(g.On)), names: g.Names, stateKey: g.stateAttribute()}
		for _, raw := range g.On {
			r.enum.on[raw] = true
		}
		if g.source() == sourceAttribute {
			r.enum.attr = g.Attribute
		}
	}
	return r
}

func sortedPairs(m map[string]string) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// decorate sets the identity of the emitted point nd from the source point dp: the index name, then
// the labels, then the constants, so a later one wins. It also notes the resource attributes.
func (r *run) decorate(c *rule, nd, dp pmetric.NumberDataPoint) {
	src, dst := dp.Attributes(), nd.Attributes()
	if c.indexAttr != "" {
		if v, ok := src.Get(c.indexAttr); ok {
			idx := clean(v.AsString())
			dst.PutStr("hw.id", idx)
			name, named := c.indexName[idx]
			if !named {
				name = idx
			}
			dst.PutStr("hw.name", name)
		}
	}
	get := func(key string) (string, bool) {
		v, ok := src.Get(key)
		if !ok {
			return "", false
		}
		return v.AsString(), true
	}
	for _, l := range c.labels {
		if text, ok := l.tpl.render(get); ok {
			dst.PutStr(l.key, clean(text))
		}
	}
	for _, p := range c.constants {
		dst.PutStr(p.key, p.value)
	}
	for _, p := range c.resource {
		r.res[p.key] = p.value
	}
}

func (r *rule) matches(attrs pcommon.Map) bool {
	for _, c := range r.attrs {
		v, ok := attrs.Get(c.key)
		if !ok || (c.value != "" && v.AsString() != c.value) {
			return false
		}
	}
	return true
}

// run holds the state of one pass over one batch.
type run struct {
	e   *engine
	ctx context.Context
	res map[string]string
}

// process rewrites md in place. With no rule it does nothing.
func (e *engine) process(ctx context.Context, md pmetric.Metrics) {
	if len(e.byMetric) == 0 {
		return
	}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		r := &run{e: e, ctx: ctx, res: map[string]string{}}
		sms := rm.ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			r.scope(sms.At(j))
		}
		for k, v := range r.res {
			rm.Resource().Attributes().PutStr(k, v)
		}
	}
}

func (r *run) scope(sm pmetric.ScopeMetrics) {
	ms := sm.Metrics()
	out := &outputs{index: map[outKey]pmetric.NumberDataPointSlice{}}
	emptied := make([]bool, ms.Len())
	for k := 0; k < ms.Len(); k++ {
		m := ms.At(k)
		rules := r.e.byMetric[m.Name()]
		if len(rules) == 0 {
			r.countUnmapped(m.Name(), pointCount(m))
			continue
		}
		var dps pmetric.NumberDataPointSlice
		switch m.Type() {
		case pmetric.MetricTypeGauge:
			dps = m.Gauge().DataPoints()
		case pmetric.MetricTypeSum:
			dps = m.Sum().DataPoints()
		default:
			r.countUnmapped(m.Name(), pointCount(m))
			continue
		}
		had := dps.Len()
		dps.RemoveIf(func(dp pmetric.NumberDataPoint) bool { return r.point(m, dp, rules, out) })
		emptied[k] = had > 0 && dps.Len() == 0
	}
	i := 0
	ms.RemoveIf(func(pmetric.Metric) bool { i++; return emptied[i-1] })
	for _, m := range out.list {
		m.MoveTo(ms.AppendEmpty())
	}
}

// point maps one point; it returns true when the source point is consumed.
func (r *run) point(src pmetric.Metric, dp pmetric.NumberDataPoint, rules []*rule, out *outputs) bool {
	var hit *rule
	for _, c := range rules {
		if c.matches(dp.Attributes()) {
			hit = c
			break
		}
	}
	val, isInt, ok := numberOf(dp)
	if hit == nil || !ok {
		r.countUnmapped(src.Name(), 1)
		return false
	}
	if hit.dropValue != nil && val == *hit.dropValue {
		return true
	}
	if hit.status != nil {
		return r.status(hit, src, dp, val, isInt, out)
	}
	if hit.enum != nil {
		return r.enumGauge(hit, src, dp, val, isInt, out)
	}
	dst := out.points(hit.target, hit.outUnit(src), src)
	nd := dst.AppendEmpty()
	copyHeader(nd, dp)
	switch {
	case hit.scaled:
		nd.SetDoubleValue(val * hit.scale)
	case isInt:
		nd.SetIntValue(dp.IntValue())
	default:
		nd.SetDoubleValue(val)
	}
	r.decorate(hit, nd, dp)
	return true
}

// status emits one hw.status point per listed state for a point of a status rule. It returns true
// when the source point is consumed: mapped, or dropped by a drop entry.
func (r *run) status(c *rule, src pmetric.Metric, dp pmetric.NumberDataPoint, val float64, isInt bool, out *outputs) bool {
	p := c.status
	var raw string
	if p.attr != "" {
		v, ok := dp.Attributes().Get(p.attr)
		if !ok {
			r.countUnmapped(src.Name(), 1)
			return false
		}
		raw = v.AsString()
	} else {
		raw = numberText(val, isInt, dp)
	}
	state, found := p.mapping[raw]
	if found && state == dropState {
		return true
	}
	usedDefault := false
	if !found {
		r.noteState(c.name, raw)
		if p.def == "" {
			return false
		}
		state, usedDefault = p.def, true
	}
	unit := c.unit
	if unit == "" {
		unit = "1"
	}
	dst := out.points(c.target, unit, src)
	for _, s := range p.states {
		nd := dst.AppendEmpty()
		copyHeader(nd, dp)
		if s == state {
			nd.SetIntValue(1)
		} else {
			nd.SetIntValue(0)
		}
		r.decorate(c, nd, dp)
		nd.Attributes().PutStr(p.stateKey, s)
		if usedDefault {
			nd.Attributes().PutStr(p.rawKey, clean(raw))
		}
	}
	return true
}

// enumGauge emits one gauge point, 1 when the raw state is in the rule's on list and 0 otherwise, with the
// state name in the state attribute. It returns true when the source point is consumed. A state that the
// names do not list is kept as read, counted and logged once.
func (r *run) enumGauge(c *rule, src pmetric.Metric, dp pmetric.NumberDataPoint, val float64, isInt bool, out *outputs) bool {
	p := c.enum
	var raw string
	if p.attr != "" {
		v, ok := dp.Attributes().Get(p.attr)
		if !ok {
			r.countUnmapped(src.Name(), 1)
			return false
		}
		raw = v.AsString()
	} else {
		raw = numberText(val, isInt, dp)
	}
	state := raw
	if len(p.names) > 0 {
		if name, listed := p.names[raw]; listed {
			state = name
		} else {
			r.noteState(c.name, raw)
		}
	}
	unit := c.unit
	if unit == "" {
		unit = "1"
	}
	nd := out.gaugePoints(c.target, unit, src).AppendEmpty()
	copyHeader(nd, dp)
	if p.on[raw] {
		nd.SetIntValue(1)
	} else {
		nd.SetIntValue(0)
	}
	r.decorate(c, nd, dp)
	nd.Attributes().PutStr(p.stateKey, clean(state))
	return true
}

// numberText writes the value of a point as the text a status map uses as its key.
func numberText(val float64, isInt bool, dp pmetric.NumberDataPoint) string {
	switch {
	case isInt:
		return strconv.FormatInt(dp.IntValue(), 10)
	case val == math.Trunc(val) && math.Abs(val) < 1e15:
		return strconv.FormatInt(int64(val), 10)
	}
	return strconv.FormatFloat(val, 'g', -1, 64)
}

// noteState counts a state text the map does not list and logs each value once.
func (r *run) noteState(ruleName, raw string) {
	e := r.e
	e.unmappedStates.Add(r.ctx, 1, metric.WithAttributes(attribute.String("rule", ruleName)))
	text := clean(raw)
	key := ruleName + "\x00" + text
	e.mu.Lock()
	_, seen := e.logged[key]
	full := len(e.logged) >= maxLoggedValues
	if !seen && !full {
		e.logged[key] = struct{}{}
	}
	announce := full && !seen && !e.capNoted
	if announce {
		e.capNoted = true
	}
	e.mu.Unlock()
	switch {
	case seen:
	case announce:
		e.logger.Warn("Too many different state texts; the new ones are counted but no longer logged", zap.String("rule", ruleName))
	case !full:
		e.logger.Warn("The state text is not in the status map", zap.String("rule", ruleName), zap.String("value", text))
	}
}

func (c *rule) outUnit(src pmetric.Metric) string {
	if c.unit != "" {
		return c.unit
	}
	return src.Unit()
}

// copyHeader gives nd the attributes, timestamps and flags of dp.
func copyHeader(nd, dp pmetric.NumberDataPoint) {
	dp.Attributes().CopyTo(nd.Attributes())
	nd.SetTimestamp(dp.Timestamp())
	nd.SetStartTimestamp(dp.StartTimestamp())
	nd.SetFlags(dp.Flags())
}

func numberOf(dp pmetric.NumberDataPoint) (val float64, isInt, ok bool) {
	switch dp.ValueType() {
	case pmetric.NumberDataPointValueTypeInt:
		return float64(dp.IntValue()), true, true
	case pmetric.NumberDataPointValueTypeDouble:
		return dp.DoubleValue(), false, true
	}
	return 0, false, false
}

func pointCount(m pmetric.Metric) int {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		return m.Gauge().DataPoints().Len()
	case pmetric.MetricTypeSum:
		return m.Sum().DataPoints().Len()
	case pmetric.MetricTypeHistogram:
		return m.Histogram().DataPoints().Len()
	case pmetric.MetricTypeExponentialHistogram:
		return m.ExponentialHistogram().DataPoints().Len()
	case pmetric.MetricTypeSummary:
		return m.Summary().DataPoints().Len()
	}
	return 0
}

// clean makes a text from a point safe to send: valid UTF-8 and at most maxTextLen bytes.
func clean(s string) string {
	if len(s) > maxTextLen {
		cut := maxTextLen
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return strings.ToValidUTF8(s, "�")
}

func (r *run) countUnmapped(name string, n int) {
	if n > 0 {
		r.e.unmappedPoints.Add(r.ctx, int64(n), metric.WithAttributes(attribute.String("metric", clean(name))))
	}
}

// outKey identifies one output metric of a scope.
type outKey struct {
	name, unit  string
	kind        pmetric.MetricType
	temporality pmetric.AggregationTemporality
	monotonic   bool
}

// outputs collects the metrics the rules emit, one per name, unit and type.
type outputs struct {
	index map[outKey]pmetric.NumberDataPointSlice
	list  []pmetric.Metric
}

func (o *outputs) points(name, unit string, src pmetric.Metric) pmetric.NumberDataPointSlice {
	key := outKey{name: name, unit: unit, kind: src.Type()}
	if src.Type() == pmetric.MetricTypeSum {
		key.temporality = src.Sum().AggregationTemporality()
		key.monotonic = src.Sum().IsMonotonic()
	}
	return o.slice(key, src)
}

// gaugePoints is points for a metric that is a gauge whatever the type of the source.
func (o *outputs) gaugePoints(name, unit string, src pmetric.Metric) pmetric.NumberDataPointSlice {
	return o.slice(outKey{name: name, unit: unit, kind: pmetric.MetricTypeGauge}, src)
}

func (o *outputs) slice(key outKey, src pmetric.Metric) pmetric.NumberDataPointSlice {
	if dps, ok := o.index[key]; ok {
		return dps
	}
	m := pmetric.NewMetric()
	m.SetName(key.name)
	m.SetUnit(key.unit)
	m.SetDescription(src.Description())
	var dps pmetric.NumberDataPointSlice
	if key.kind == pmetric.MetricTypeSum {
		s := m.SetEmptySum()
		s.SetAggregationTemporality(key.temporality)
		s.SetIsMonotonic(key.monotonic)
		dps = s.DataPoints()
	} else {
		dps = m.SetEmptyGauge().DataPoints()
	}
	o.index[key] = dps
	o.list = append(o.list, m)
	return dps
}
