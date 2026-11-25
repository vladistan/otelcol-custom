// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/otel/metric"
)

// Glue mode joins the fragments of one console line that the console wrapped at a width. Only the
// first fragment carries the tag that starts a line; the rest are cut inside a token, and each
// ends like a whole line. The end of a fragment is no boundary, so the rules read the text: a row
// continues the open sequence of its node when it is not a first entry, not a standalone line and
// the fragment before it reached the wrap width.

const (
	scopeName          = "github.com/vladistan/otelcol-custom/processor/logrecombineprocessor"
	gluedMetric        = "otelcol_logrecombine_glued"
	uidNotRisingMetric = "otelcol_logrecombine_uid_not_rising"
	emptyDroppedMetric = "otelcol_logrecombine_empty_dropped"
	uidAttribute       = "log.record.uid"
	originalAttribute  = "log.record.original"
)

type glueTelemetry struct {
	glued, uidNotRising, emptyDropped metric.Int64Counter
}

func newGlueTelemetry(ts component.TelemetrySettings) (*glueTelemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	glued, err := meter.Int64Counter(gluedMetric,
		metric.WithDescription("Log records made of two or more fragments."), metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	notRising, err := meter.Int64Counter(uidNotRisingMetric,
		metric.WithDescription("Log records whose uid did not rise above the previous one of their node."), metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter(emptyDroppedMetric,
		metric.WithDescription("Empty log records dropped after the glue."), metric.WithUnit("{record}"))
	if err != nil {
		return nil, err
	}
	return &glueTelemetry{glued: glued, uidNotRising: notRising, emptyDropped: dropped}, nil
}

// glueSeq is the open sequence of one node: the record of its first fragment and the joined text.
type glueSeq struct {
	record    plog.LogRecord
	resource  pcommon.Resource
	scope     pcommon.InstrumentationScope
	text      []byte
	fragments int
	lastLen   int // length of the text of the last fragment, its end included
	firstSeen time.Time
	lastSeen  time.Time
}

// glueNode is what the processor knows of one node.
type glueNode struct {
	seq      *glueSeq
	uid      uint64
	hasUID   bool
	lastSeen time.Time
}

type glue struct {
	requireAttr string
	neverGlue   []*regexp.Regexp
	empty       []*regexp.Regexp
	minLen      int
	suffix      []byte
	maxSize     int
	tel         *glueTelemetry
	nodes       map[string]*glueNode
}

func newGlue(cfg *Config, ts component.TelemetrySettings) (*glue, error) {
	if cfg.RequireAttribute == "" {
		return nil, nil
	}
	tel, err := newGlueTelemetry(ts)
	if err != nil {
		return nil, err
	}
	g := &glue{
		requireAttr: cfg.RequireAttribute,
		minLen:      cfg.MinPreviousLength,
		suffix:      []byte(cfg.FragmentSuffix),
		maxSize:     cfg.MaxTextSize,
		tel:         tel,
		nodes:       make(map[string]*glueNode),
	}
	if g.neverGlue, err = compileAll(cfg.NeverGluePatterns); err != nil {
		return nil, err
	}
	if g.empty, err = compileAll(cfg.EmptyPatterns); err != nil {
		return nil, err
	}
	return g, nil
}

func compileAll(exprs []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(exprs))
	for _, e := range exprs {
		re, err := regexp.Compile(e)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

func matchesAny(res []*regexp.Regexp, text string) bool {
	for _, re := range res {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// marked reports whether the record carries the required attribute, as a bool or as the text true.
func (g *glue) marked(record plog.LogRecord) bool {
	v, ok := record.Attributes().Get(g.requireAttr)
	if !ok {
		return false
	}
	switch v.Type() {
	case pcommon.ValueTypeBool:
		return v.Bool()
	case pcommon.ValueTypeStr:
		return strings.EqualFold(v.Str(), "true")
	}
	return false
}

// process takes one record. The caller holds the lock of the processor.
func (p *logsProcessor) processGlue(ctx context.Context, out plog.Logs, record plog.LogRecord, resource pcommon.Resource, scope pcommon.InstrumentationScope) {
	g := p.glue
	pass := func() { p.addRecordToLogs(out, record, resource, scope) }
	if !g.marked(record) || record.Body().Type() != pcommon.ValueTypeStr {
		pass()
		return
	}
	key := p.bufferKey(record, resource)
	if key == "" {
		pass()
		return
	}
	now := time.Now()
	text := record.Body().Str()
	node := p.glueNodeFor(out, key, now)
	g.checkUID(ctx, node, record)

	switch {
	case text == "" || matchesAny(g.empty, text):
		p.flushGlueSeq(ctx, out, node)
		g.tel.emptyDropped.Add(ctx, 1)
	case matchesAny(g.neverGlue, text):
		p.flushGlueSeq(ctx, out, node)
		pass()
	case p.firstEntryRe.MatchString(text):
		p.flushGlueSeq(ctx, out, node)
		node.seq = newGlueSeq(record, resource, scope, text, now)
	case node.seq != nil && node.seq.lastLen >= g.minLen:
		if len(node.seq.text)+len(text) > g.maxSize {
			p.flushGlueSeq(ctx, out, node)
			node.seq = newGlueSeq(record, resource, scope, text, now)
			return
		}
		g.append(node.seq, text, now)
	default:
		p.flushGlueSeq(ctx, out, node)
		if len(text) >= g.minLen {
			node.seq = newGlueSeq(record, resource, scope, text, now)
			return
		}
		pass()
	}
}

func newGlueSeq(record plog.LogRecord, resource pcommon.Resource, scope pcommon.InstrumentationScope, text string, now time.Time) *glueSeq {
	s := &glueSeq{
		record:    plog.NewLogRecord(),
		resource:  pcommon.NewResource(),
		scope:     pcommon.NewInstrumentationScope(),
		text:      []byte(text),
		fragments: 1,
		lastLen:   len(text),
		firstSeen: now,
		lastSeen:  now,
	}
	record.CopyTo(s.record)
	resource.CopyTo(s.resource)
	scope.CopyTo(s.scope)
	return s
}

// append adds a fragment: the end of the fragment before it goes, the end of this one stays.
func (g *glue) append(s *glueSeq, text string, now time.Time) {
	if len(g.suffix) > 0 && bytes.HasSuffix(s.text, g.suffix) {
		s.text = s.text[:len(s.text)-len(g.suffix)]
	}
	s.text = append(s.text, text...)
	s.fragments++
	s.lastLen = len(text)
	s.lastSeen = now
}

// checkUID counts a record whose uid is not above the previous one of its node. It still glues.
func (g *glue) checkUID(ctx context.Context, node *glueNode, record plog.LogRecord) {
	v, ok := record.Attributes().Get(uidAttribute)
	if !ok {
		return
	}
	uid, err := strconv.ParseUint(v.AsString(), 10, 64)
	if err != nil {
		return
	}
	if node.hasUID && uid <= node.uid {
		g.tel.uidNotRising.Add(ctx, 1)
	}
	node.uid, node.hasUID = uid, true
}

// glueNodeFor returns the state of a node and, when the table is full, passes on the oldest other node.
func (p *logsProcessor) glueNodeFor(out plog.Logs, key string, now time.Time) *glueNode {
	g := p.glue
	node, ok := g.nodes[key]
	if !ok {
		node = &glueNode{}
		g.nodes[key] = node
		if len(g.nodes) > p.config.MaxSources {
			p.evictGlueNode(out, key)
		}
	}
	node.lastSeen = now
	return node
}

func (p *logsProcessor) evictGlueNode(out plog.Logs, keep string) {
	var oldestKey string
	var oldest time.Time
	for key, n := range p.glue.nodes {
		if key == keep {
			continue
		}
		if oldestKey == "" || n.lastSeen.Before(oldest) {
			oldestKey, oldest = key, n.lastSeen
		}
	}
	if oldestKey == "" {
		return
	}
	p.flushGlueSeq(context.Background(), out, p.glue.nodes[oldestKey])
	delete(p.glue.nodes, oldestKey)
}

// flushGlueSeq passes on the open sequence of a node, if any.
func (p *logsProcessor) flushGlueSeq(ctx context.Context, out plog.Logs, node *glueNode) {
	s := node.seq
	if s == nil {
		return
	}
	node.seq = nil
	if s.fragments > 1 {
		joined := string(s.text)
		s.record.Body().SetStr(joined)
		if _, ok := s.record.Attributes().Get(originalAttribute); ok {
			s.record.Attributes().PutStr(originalAttribute, joined)
		}
		p.glue.tel.glued.Add(ctx, 1)
	}
	rl := out.ResourceLogs().AppendEmpty()
	s.resource.CopyTo(rl.Resource())
	sl := rl.ScopeLogs().AppendEmpty()
	s.scope.CopyTo(sl.Scope())
	s.record.CopyTo(sl.LogRecords().AppendEmpty())
}

// flushStaleGlue passes on the sequences whose last fragment is older than the gap or whose first is
// older than the maximum time.
func (p *logsProcessor) flushStaleGlue(out plog.Logs, now time.Time) {
	for _, node := range p.glue.nodes {
		s := node.seq
		if s != nil && (now.Sub(s.lastSeen) > p.config.GapTimeout || now.Sub(s.firstSeen) > p.config.MaxTimeout) {
			p.flushGlueSeq(context.Background(), out, node)
		}
	}
}

// flushAllGlue passes on every open sequence.
func (p *logsProcessor) flushAllGlue(out plog.Logs) {
	for _, node := range p.glue.nodes {
		p.flushGlueSeq(context.Background(), out, node)
	}
}
