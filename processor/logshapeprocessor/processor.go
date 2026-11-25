// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"runtime/debug"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

type shapeProcessor struct {
	branches []branch
	tbl      *table
	cfg      *Config
	hints    map[string]Hint
	next     consumer.Logs
	tel      *telemetry
	logger   *zap.Logger
	// draw is the random source of the autoscaler sampling, a number in [0, 1).
	draw func() float64
	// lineServices is the set of service names of severity_from_line.services.
	lineServices map[string]struct{}
	// panicked holds the branches whose panic was logged: each is logged once.
	panicked sync.Map
}

// matches reports whether every field that is set equals the service, scope or source of the record.
func (h Hint) matches(res pcommon.Resource, scope pcommon.InstrumentationScope, lr plog.LogRecord) bool {
	if h.Service != "" && stringAttr(res.Attributes(), serviceKey) != h.Service {
		return false
	}
	if h.Scope != "" && scope.Name() != h.Scope {
		return false
	}
	if h.Source != "" && stringAttr(lr.Attributes(), "log.source") != h.Source {
		return false
	}
	return true
}

func newProcessor(set processor.Settings, cfg *Config, next consumer.Logs) (*shapeProcessor, error) {
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	branches, err := newBranches(cfg)
	if err != nil {
		return nil, err
	}
	p := &shapeProcessor{cfg: cfg, hints: cfg.Hints, next: next, tel: tel, logger: set.Logger, draw: rand.Float64}
	if n := len(cfg.SeverityFromLine.Services); n > 0 {
		p.lineServices = make(map[string]struct{}, n)
		for _, s := range cfg.SeverityFromLine.Services {
			p.lineServices[s] = struct{}{}
		}
	}
	p.setBranches(branches)
	return p, nil
}

// setBranches sets the branches in claim order and builds the table that picks the ones to try.
func (p *shapeProcessor) setBranches(branches []branch) {
	p.branches = branches
	p.tbl = newTable(branches, p.hints, p.cfg)
}

func (p *shapeProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *shapeProcessor) Start(context.Context, component.Host) error { return nil }

func (p *shapeProcessor) Shutdown(context.Context) error { return nil }

// ConsumeLogs shapes every record. A branch may change the resource (host.name, service.name) or the
// scope (its name), so each record moves under the ResourceLogs and ScopeLogs that carry what the
// branch left. The input resource and scope are only copied, never changed.
func (p *shapeProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	out := plog.NewLogs()
	unmatched, failed, sampled := map[string]int64{}, map[string]int64{}, map[string]int64{}
	r, w := new(rec), newWork()
	for i := range ld.ResourceLogs().Len() {
		rl := ld.ResourceLogs().At(i)
		w.resDirty = true
		// Pass 1: shape every record in place and note the few that changed their resource or scope.
		var changed []routed
		idx := 0
		for j := range rl.ScopeLogs().Len() {
			sl := rl.ScopeLogs().At(j)
			w.scopeDirty = true
			for k := range sl.LogRecords().Len() {
				lr := sl.LogRecords().At(k)
				to := p.route(w, r, rl.Resource(), sl.Scope(), lr, unmatched, failed, sampled)
				if p.lineServices != nil && !to.drop {
					res := rl.Resource()
					if to.resChanged {
						res = to.res
					}
					p.levelFromLine(res, lr)
				}
				if to.resChanged || to.scopeChanged || to.drop {
					if changed == nil {
						changed = make([]routed, recordCount(rl))
					}
					changed[idx] = to
				}
				idx++
			}
		}
		if changed == nil {
			rl.MoveTo(out.ResourceLogs().AppendEmpty())
			continue
		}
		// Pass 2: some record needs its own resource or scope, so regroup.
		groups := newGroups(out, rl)
		idx = 0
		for j := range rl.ScopeLogs().Len() {
			sl := rl.ScopeLogs().At(j)
			for k := range sl.LogRecords().Len() {
				if !changed[idx].drop {
					groups.add(j, sl, changed[idx], sl.LogRecords().At(k))
				}
				idx++
			}
		}
	}
	p.tel.flush(ctx, unmatched, failed, sampled)
	return p.next.ConsumeLogs(ctx, out)
}

func recordCount(rl plog.ResourceLogs) int {
	n := 0
	for i := range rl.ScopeLogs().Len() {
		n += rl.ScopeLogs().At(i).LogRecords().Len()
	}
	return n
}

// routed is where a record goes after routing. Resource and Scope are set only when the branch changed them.
// A dropped record is left out of the output.
type routed struct {
	res   pcommon.Resource
	scope pcommon.InstrumentationScope

	resChanged, scopeChanged, drop bool
}

// route sends the record to the first branch that claims it. The branch works on the scratch objects of
// w, and the scratch record replaces the real one only when the parse succeeds.
func (p *shapeProcessor) route(w *work, r *rec, res pcommon.Resource, scope pcommon.InstrumentationScope, lr plog.LogRecord, unmatched, failed, sampled map[string]int64) routed {
	*r = rec{lr: lr, res: res, scope: scope, body: bodyText(lr)}
	var s seen
	for _, i := range p.tbl.candidates(r.body) {
		e := &p.tbl.entries[i]
		if !e.g.admits(r, &s) || (e.hint != nil && !e.hint.matches(res, scope, lr)) {
			continue
		}
		b := e.b
		claimed, err := safeClaims(b, r)
		if err != nil {
			p.noteFailure(b.name(), err, failed)
			return routed{}
		}
		if !claimed {
			continue
		}
		saved := saveMapBody(lr)
		sr := w.prepare(lr, res, scope, r.body, r.memo)
		if err := safeParse(b, sr); err != nil {
			w.resDirty, w.scopeDirty = true, true // the failed parse may have written them
			p.noteFailure(b.name(), err, failed)
			return routed{}
		}
		if b.name() == "syslog" {
			p.parseInner(sr, failed)
		}
		if b.name() == "autoscaler" && p.sampledOut(sr) {
			sampled[b.name()]++
			return routed{drop: true}
		}
		w.lr.MoveTo(lr)
		saved.restore(lr)
		out := routed{}
		if !mapsEqual(res.Attributes(), w.res.Attributes()) {
			out.res, out.resChanged = w.changedResource(), true
			w.resDirty = true
		}
		if !scopesEqual(scope, w.scope) {
			out.scope, out.scopeChanged = w.changedScope(), true
			w.scopeDirty = true
		}
		return out
	}
	if !s.haveSrc {
		s.src = sourceOf(lr, res)
	}
	unmatched[s.src]++
	return routed{}
}

// parseInner runs the branches after a syslog unwrap on the inner text, so a wrapped smartd or agent
// line gets its own severity and body. The wrapper's original line stays. A failed inner parse
// keeps the unwrapped record.
func (p *shapeProcessor) parseInner(sr *rec, failed map[string]int64) {
	sr.body = bodyText(sr.lr)
	sr.memo = nil
	if sr.body == "" {
		return
	}
	orig, hadOrig := sr.lr.Attributes().Get(originalKey)
	origText := orig.AsString()
	var s seen
	for _, i := range p.tbl.candidates(sr.body) {
		e := &p.tbl.entries[i]
		b := e.b
		if b.name() == "syslog" || !e.g.admits(sr, &s) {
			continue
		}
		if e.hint != nil && !e.hint.matches(sr.res, sr.scope, sr.lr) {
			continue
		}
		if claimed, err := safeClaims(b, sr); err != nil || !claimed {
			continue
		}
		scratch := plog.NewLogRecord()
		sr.lr.CopyTo(scratch)
		scratchRes := pcommon.NewResource()
		sr.res.CopyTo(scratchRes)
		scratchScope := pcommon.NewInstrumentationScope()
		sr.scope.CopyTo(scratchScope)
		if err := safeParse(b, &rec{lr: scratch, res: scratchRes, scope: scratchScope, body: sr.body, memo: sr.memo}); err != nil {
			p.noteFailure(b.name(), err, failed)
			return
		}
		scratch.CopyTo(sr.lr)
		scratchRes.CopyTo(sr.res)
		scratchScope.CopyTo(sr.scope)
		if hadOrig {
			sr.lr.Attributes().PutStr(originalKey, origText)
		}
		return
	}
}

// scopeKey finds the scopes of one group that may equal a record's scope: the input scope position
// for a record whose scope no branch changed, else the hash of the changed scope.
type scopeKey struct {
	idx     int
	changed bool
	hash    uint64
}

type scopeEntry struct {
	scope pcommon.InstrumentationScope
	sl    plog.ScopeLogs
}

// groups collects the records of one input ResourceLogs under one output ResourceLogs per distinct
// resource and one ScopeLogs per distinct scope inside it, in first-seen order.
type groups struct {
	out     plog.Logs
	from    plog.ResourceLogs
	last    *group              // the group of the previous changed resource
	lastRes pcommon.Resource    // that resource, to find the same one again by identity
	same    *group              // records that kept the input resource
	byHash  map[uint64][]*group // records whose branch changed the resource, found by its hash
}

type group struct {
	rl     plog.ResourceLogs
	scopes map[scopeKey][]scopeEntry

	// the ScopeLogs of the previous record whose scope a branch changed
	hasLast   bool
	lastIdx   int
	lastScope pcommon.InstrumentationScope
	lastSL    plog.ScopeLogs
}

func newGroups(out plog.Logs, from plog.ResourceLogs) *groups {
	return &groups{out: out, from: from, byHash: map[uint64][]*group{}}
}

func (g *groups) newGroup(res pcommon.Resource) *group {
	rl := g.out.ResourceLogs().AppendEmpty()
	rl.SetSchemaUrl(g.from.SchemaUrl())
	res.CopyTo(rl.Resource())
	return &group{rl: rl, scopes: map[scopeKey][]scopeEntry{}}
}

// groupFor finds the group of a record's resource; the hash only narrows the search, mapsEqual decides.
func (g *groups) groupFor(to routed) *group {
	if !to.resChanged {
		if g.same == nil {
			g.same = g.newGroup(g.from.Resource())
		}
		return g.same
	}
	if g.last != nil && to.res == g.lastRes {
		return g.last // the same kept resource as the record before: no lookup
	}
	hash := hashResource(to.res.Attributes())
	for _, grp := range g.byHash[hash] {
		if mapsEqual(grp.rl.Resource().Attributes(), to.res.Attributes()) {
			g.last, g.lastRes = grp, to.res
			return grp
		}
	}
	grp := g.newGroup(to.res)
	g.byHash[hash] = append(g.byHash[hash], grp)
	g.last, g.lastRes = grp, to.res
	return grp
}

// scopeLogs finds the ScopeLogs of a record; a changed scope that is the same kept object as the
// record before needs no lookup.
func (grp *group) scopeLogs(idx int, from plog.ScopeLogs, to routed) plog.ScopeLogs {
	if to.scopeChanged && grp.hasLast && idx == grp.lastIdx && to.scope == grp.lastScope {
		return grp.lastSL
	}
	sl := grp.findScopeLogs(idx, from, to)
	if to.scopeChanged {
		grp.hasLast, grp.lastIdx, grp.lastScope, grp.lastSL = true, idx, to.scope, sl
	}
	return sl
}

func (grp *group) findScopeLogs(idx int, from plog.ScopeLogs, to routed) plog.ScopeLogs {
	key := scopeKey{idx: idx}
	if to.scopeChanged {
		key.changed, key.hash = true, hashScopeOf(to.scope)
	}
	for _, e := range grp.scopes[key] {
		if !to.scopeChanged || scopesEqual(e.scope, to.scope) {
			return e.sl
		}
	}
	sl := grp.rl.ScopeLogs().AppendEmpty()
	if to.scopeChanged {
		to.scope.CopyTo(sl.Scope())
	} else {
		from.Scope().CopyTo(sl.Scope())
	}
	sl.SetSchemaUrl(from.SchemaUrl())
	grp.scopes[key] = append(grp.scopes[key], scopeEntry{scope: sl.Scope(), sl: sl})
	return sl
}

func (g *groups) add(scopeIdx int, from plog.ScopeLogs, to routed, lr plog.LogRecord) {
	sl := g.groupFor(to).scopeLogs(scopeIdx, from, to)
	lr.MoveTo(sl.LogRecords().AppendEmpty())
}

func scopesEqual(a, b pcommon.InstrumentationScope) bool {
	return a.Name() == b.Name() && a.Version() == b.Version() &&
		a.DroppedAttributesCount() == b.DroppedAttributesCount() && mapsEqual(a.Attributes(), b.Attributes())
}

// mapsEqual compares two attribute maps without copying scalar values.
func mapsEqual(a, b pcommon.Map) bool {
	if a.Len() != b.Len() {
		return false
	}
	equal := true
	a.Range(func(k string, av pcommon.Value) bool {
		bv, ok := b.Get(k)
		equal = ok && valuesEqual(av, bv)
		return equal
	})
	return equal
}

func valuesEqual(a, b pcommon.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch a.Type() {
	case pcommon.ValueTypeStr:
		return a.Str() == b.Str()
	case pcommon.ValueTypeInt:
		return a.Int() == b.Int()
	case pcommon.ValueTypeDouble:
		return a.Double() == b.Double()
	case pcommon.ValueTypeBool:
		return a.Bool() == b.Bool()
	case pcommon.ValueTypeBytes:
		return bytes.Equal(a.Bytes().AsRaw(), b.Bytes().AsRaw())
	case pcommon.ValueTypeMap:
		return mapsEqual(a.Map(), b.Map())
	case pcommon.ValueTypeEmpty:
		return true
	}
	return reflect.DeepEqual(a.AsRaw(), b.AsRaw())
}

// branchPanic is a panic recovered from a branch.
type branchPanic struct {
	value any
	stack string
}

func (e *branchPanic) Error() string { return fmt.Sprint("branch panicked: ", e.value) }

// recoverBranch turns a panic into an error. It must be the deferred function itself.
func recoverBranch(err *error) {
	if v := recover(); v != nil {
		*err = &branchPanic{value: v, stack: string(debug.Stack())}
	}
}

// safeClaims calls claims; a panic in it is an error.
func safeClaims(b branch, r *rec) (claimed bool, err error) {
	defer recoverBranch(&err)
	return b.claims(r), nil
}

// safeParse calls parse; a panic in it is an error.
func safeParse(b branch, r *rec) (err error) {
	defer recoverBranch(&err)
	return b.parse(r)
}

// noteFailure counts a record a branch failed on. A panic is logged once for each branch, with its stack,
// and the record stays as it was.
func (p *shapeProcessor) noteFailure(name string, err error, failed map[string]int64) {
	failed[name]++
	var bp *branchPanic
	if !errors.As(err, &bp) {
		return
	}
	if _, seen := p.panicked.LoadOrStore(name, struct{}{}); seen {
		return
	}
	p.logger.Error("a logshape branch panicked; the record is left unchanged",
		zap.String("branch", name), zap.String("panic", fmt.Sprint(bp.value)), zap.String("stack", bp.stack))
}
