// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	batchSize      = 500
	futureSkew     = 5 * time.Minute
	saveEvery      = time.Second
	minRescanGap   = time.Minute
	quotaWindow    = time.Hour
	debtPoll       = 5 * time.Second
	minSettleGap   = 250 * time.Millisecond
	logBody        = "created"
	completePrefix = "write complete "
	sourceName     = "fswatch"
)

type stateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
}

type watcher interface {
	Messages() <-chan watchMsg
	Close() error
}

type openFunc func(root string, bufferSize int) (watcher, error)

type candidate struct {
	path      string
	size      int64 // -1 when unknown
	created   time.Time
	modified  time.Time // last write time; zero when unknown
	catchup   bool
	sizeFinal int64 // -1 when not settled
	complete  bool  // a write-complete record, not a creation record
}

type pendingFile struct {
	c          candidate
	lastSize   int64
	lastChange time.Time
	firstSeen  time.Time
	// failedSince and attempts track failed write-complete deliveries.
	failedSince time.Time
	attempts    int
}

type rootParams struct {
	cfg    *Config
	root   string
	name   string // service.name of the root's records; empty uses the root's last path element
	logger *zap.Logger
	next   consumer.Logs
	tel    *telemetry
	tree   tree
	store  stateStore
	open   openFunc
	now    func() time.Time
	match  *matcher
}

// rootWatch owns one watched root. All methods run on its single goroutine.
type rootWatch struct {
	rootParams
	key string

	w          watcher
	backoffMin time.Duration
	backoffMax time.Duration

	sentinel   time.Time
	maxEmitted time.Time
	// unsentFrom is the oldest creation time among files an emit call has not delivered yet.
	unsentFrom time.Time
	seen       map[seenKey]time.Time
	// done holds files whose last record is out, kept while a restart could still check them.
	done    map[seenKey]struct{}
	pending map[string]*pendingFile
	lastOld string

	uncapped  bool
	frozen    bool
	rescanDue bool
	// recheckDue asks the first scan after a start with saved state to follow recent files again.
	recheckDue bool

	rescans    []time.Time
	lastRescan time.Time
	lastScan   time.Time
	cappedWarn time.Time
	failWarn   time.Time

	dirty      bool
	readFailed bool
	lastSave   time.Time
	setWarned  bool
	openDown   bool
	openErrMsg string
}

func newRootWatch(p rootParams) *rootWatch {
	if p.name == "" {
		p.name = defaultRootName(p.root)
	}
	return &rootWatch{
		rootParams: p,
		key:        rootKey(p.root),
		seen:       map[seenKey]time.Time{},
		done:       map[seenKey]struct{}{},
		pending:    map[string]*pendingFile{},
		backoffMin: 5 * time.Second,
		backoffMax: time.Minute,
	}
}

// begin loads the saved state, or applies the first-start rule when there is none.
func (rw *rootWatch) begin(ctx context.Context) {
	if st, ok := rw.loadState(ctx); ok {
		rw.sentinel = time.Unix(0, st.Sentinel)
		rw.maxEmitted = rw.sentinel
		for _, e := range st.Recent {
			rw.seen[seenKey{path: e.Path, created: e.Created}] = time.Unix(0, e.Created)
		}
		for _, e := range st.Done {
			rw.done[seenKey{path: e.Path, created: e.Created}] = struct{}{}
		}
		rw.recheckDue = true
		return
	}
	if rw.cfg.StartAt == startAtBeginning {
		rw.uncapped = true
		return
	}
	rw.sentinel = rw.now()
	rw.maxEmitted = rw.sentinel
	rw.dirty = !rw.readFailed
	rw.flush(ctx, true)
}

func (rw *rootWatch) loadState(ctx context.Context) (persisted, bool) {
	if rw.store == nil {
		rw.logger.Warn("no storage extension is set; the sentinel is lost when the collector stops", zap.String("root", rw.root))
		return persisted{}, false
	}
	raw, err := rw.store.Get(ctx, rw.key)
	if err != nil {
		rw.readFailed = true
		rw.logger.Warn("storage read failed; treating this as a first start", zap.String("root", rw.root), zap.Error(err))
		return persisted{}, false
	}
	if len(raw) == 0 {
		return persisted{}, false
	}
	st, err := parseState(raw)
	if err != nil {
		rw.logger.Warn("saved state is unreadable; treating this as a first start", zap.String("root", rw.root), zap.Error(err))
		return persisted{}, false
	}
	return st, true
}

func (rw *rootWatch) rel(path string) string {
	if r, err := filepath.Rel(rw.root, path); err == nil {
		return r
	}
	return filepath.Base(path)
}

func (rw *rootWatch) keyOf(c candidate) (seenKey, bool) {
	if c.created.IsZero() {
		return seenKey{}, false
	}
	return seenKey{path: strings.ToLower(c.path), created: c.created.UnixNano()}, true
}

// catchUp emits the files created after the sentinel. A failed delivery leaves the debt flags set.
func (rw *rootWatch) catchUp(ctx context.Context) error {
	now := rw.now()
	rw.lastScan = now
	floor := rw.sentinel.Add(-recentWindow)
	since := floor
	limited := false
	if !rw.uncapped {
		if limit := now.Add(-rw.cfg.MaxCatchup); limit.After(since) {
			since, limited = limit, true
		}
	}

	var found, recent []candidate
	var cutOff, future int
	stats, err := rw.tree.Walk(ctx, rw.root, rw.prune(ctx, rw.root), func(f fileInfo) {
		if rw.match.excluded(rw.rel(f.path)) {
			rw.countExcluded(ctx)
			return
		}
		if rw.recheckDue && rw.cfg.Settle > 0 && !f.created.IsZero() && !f.created.After(now) && now.Sub(f.created) < rw.cfg.SettleTimeout {
			recent = append(recent, candidate{path: f.path, size: f.size, created: f.created, modified: f.modified, catchup: true, sizeFinal: -1})
		}
		switch {
		case f.created.After(since):
			if f.created.After(now.Add(futureSkew)) {
				future++
				return
			}
			c := candidate{path: f.path, size: f.size, created: f.created, modified: f.modified, catchup: true, sizeFinal: -1}
			if rw.cfg.Settle > 0 && !f.modified.IsZero() && now.Sub(f.modified) >= rw.cfg.Settle {
				c.sizeFinal = f.size // already stable: the one record carries the final size
			}
			found = append(found, c)
		case limited && f.created.After(floor):
			cutOff++
		}
	})
	if err != nil {
		rw.rescanDue = true
		return err
	}
	rw.tel.skipped(ctx, "denied", int64(stats.denied))
	rw.tel.skipped(ctx, "future", int64(future))
	if stats.denied > 0 {
		rw.logger.Warn("scan skipped entries that denied access", zap.String("root", rw.root), zap.Int("count", stats.denied))
	}
	if cutOff > 0 {
		rw.logger.Warn("catch-up stopped at max_catchup; older files were not reported",
			zap.String("root", rw.root), zap.Int("files", cutOff), zap.Duration("max_catchup", rw.cfg.MaxCatchup))
	}

	if err := rw.emit(ctx, found); err != nil {
		return err
	}
	rw.uncapped = false
	rw.recheck(now, recent)
	return nil
}

// recheck follows files reported before a restart that were created within settle_timeout, unless
// the scan just reported them or their last record is already out. A file whose last write is older than settle gets its write-complete
// record at the next check; one written more recently is watched as usual. It runs once per start.
func (rw *rootWatch) recheck(now time.Time, files []candidate) {
	if !rw.recheckDue {
		return
	}
	rw.recheckDue = false
	for _, c := range files {
		if _, watched := rw.pending[strings.ToLower(c.path)]; watched {
			continue
		}
		if k, ok := rw.keyOf(c); ok {
			if _, finished := rw.done[k]; finished {
				continue
			}
		}
		rw.watch(c, now)
	}
}

// watch starts following a delivered file's size, live or caught up alike, unless its creation record
// already carries a final size. The size counts as
// unchanged since the file's last write, and the watch started when the file was created.
func (rw *rootWatch) watch(c candidate, now time.Time) {
	if rw.cfg.Settle <= 0 || (c.sizeFinal >= 0 && !c.complete) {
		return
	}
	lastChange, firstSeen := now, now
	if !c.modified.IsZero() && c.modified.Before(now) {
		lastChange = c.modified
	}
	if !c.created.IsZero() && c.created.Before(now) {
		firstSeen = c.created
	}
	rw.pending[strings.ToLower(c.path)] = &pendingFile{c: c, lastSize: c.size, lastChange: lastChange, firstSeen: firstSeen}
}

// emit drops files already seen, then sends the rest oldest first in batches of at most batchSize.
// The sentinel stays below every file not yet delivered, so a failed batch is found again by a rescan.
func (rw *rootWatch) emit(ctx context.Context, cands []candidate) error {
	defer func() { rw.unsentFrom = time.Time{} }()
	fresh := make([]candidate, 0, len(cands))
	inflight := map[seenKey]struct{}{}
	for _, c := range cands {
		if k, ok := rw.keyOf(c); ok {
			if _, dup := rw.seen[k]; dup {
				continue
			}
			if _, dup := inflight[k]; dup {
				continue
			}
			inflight[k] = struct{}{}
		}
		fresh = append(fresh, c)
	}
	sortCandidates(fresh)
	for len(fresh) > 0 {
		n := min(len(fresh), batchSize)
		rw.unsentFrom = oldestCreated(fresh[n:])
		if err := rw.send(ctx, fresh[:n]); err != nil {
			return err
		}
		fresh = fresh[n:]
	}
	return nil
}

// oldestCreated is the earliest known creation time, or the zero time when there is none.
func oldestCreated(cands []candidate) time.Time {
	var oldest time.Time
	for _, c := range cands {
		if !c.created.IsZero() && (oldest.IsZero() || c.created.Before(oldest)) {
			oldest = c.created
		}
	}
	return oldest
}

func (rw *rootWatch) send(ctx context.Context, batch []candidate) error {
	observed := rw.now()
	if err := rw.next.ConsumeLogs(ctx, rw.buildLogs(batch, observed)); err != nil {
		rw.frozen, rw.rescanDue = true, true
		rw.warnDelivery(err, observed, "delivery failed; the sentinel is held until a rescan succeeds")
		return err
	}
	var live, caught int64
	for _, c := range batch {
		if k, ok := rw.keyOf(c); ok {
			rw.seen[k] = c.created
			if c.created.After(rw.maxEmitted) {
				rw.maxEmitted = c.created
			}
		}
		if c.sizeFinal >= 0 {
			rw.markDone(c)
		}
		rw.watch(c, observed)
		if c.catchup {
			caught++
		} else {
			live++
		}
	}
	rw.tel.emit(ctx, false, live)
	rw.tel.emit(ctx, true, caught)
	rw.dirty = true
	rw.advance()
	return nil
}

// warnDelivery logs a failed delivery at most once a minute.
func (rw *rootWatch) warnDelivery(err error, now time.Time, msg string) {
	if rw.failWarn.IsZero() || now.Sub(rw.failWarn) >= time.Minute {
		rw.failWarn = now
		rw.logger.Warn(msg, zap.String("root", rw.root), zap.Error(err))
	}
}

// buildLogs makes one record per candidate: a creation record, or a write-complete record when
// complete is set. A write-complete record is stamped with the time the receiver ended the watch.
// The resource carries the root's service name.
func (rw *rootWatch) buildLogs(batch []candidate, observed time.Time) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", rw.name)
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName(scopeName)
	for _, c := range batch {
		lr := sl.LogRecords().AppendEmpty()
		size := c.size
		if c.sizeFinal >= 0 {
			size = c.sizeFinal
		}
		rel := rw.rel(c.path)
		ts := c.created
		lr.Body().SetStr(logBody)
		if c.complete {
			lr.Body().SetStr(completeBody(slashesToBackslashes(rel), size))
			ts = observed
		}
		lr.SetSeverityNumber(plog.SeverityNumberInfo)
		lr.SetSeverityText("INFO")
		if ts.IsZero() {
			ts = observed
		}
		lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
		lr.SetObservedTimestamp(pcommon.NewTimestampFromTime(observed))

		name := filepath.Base(c.path)
		attrs := lr.Attributes()
		attrs.PutStr("log.source", sourceName)
		attrs.PutStr("file.path", c.path)
		attrs.PutStr("file.directory", filepath.Dir(c.path))
		attrs.PutStr("file.name", name)
		attrs.PutStr("file.extension", strings.TrimPrefix(filepath.Ext(name), "."))
		if size >= 0 {
			attrs.PutInt("file.size", size)
		}
		attrs.PutStr("vstan.fswatch.dir_relative", filepath.Dir(rel))
		attrs.PutStr("vstan.fswatch.root", rw.root)
		attrs.PutBool("vstan.fswatch.catchup", c.catchup)
	}
	return ld
}

// completeBody is the write-complete body: the root-relative path and, when known, the size.
func completeBody(rel string, size int64) string {
	if size < 0 {
		return completePrefix + rel
	}
	return completePrefix + rel + " (" + formatSize(size) + ")"
}

// formatSize writes a byte count in binary units with one decimal; under 1 KiB it is whole bytes.
func formatSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	value, unit := float64(n), 0
	for value >= 1024 && unit < 4 {
		value /= 1024
		unit++
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + [...]string{"B", "KiB", "MiB", "GiB", "TiB"}[unit]
}

// advance moves the sentinel up to the newest emitted file. It never passes the wall clock
// or a file an emit call has not delivered yet.
func (rw *rootWatch) advance() {
	if rw.frozen {
		return
	}
	target := rw.maxEmitted
	if now := rw.now(); target.After(now) {
		target = now
	}
	if !rw.unsentFrom.IsZero() && !rw.unsentFrom.After(target) {
		target = rw.unsentFrom.Add(-time.Nanosecond)
	}
	if !target.After(rw.sentinel) {
		return
	}
	rw.sentinel = target
	rw.dirty = true
	floor := target.Add(-recentWindow).UnixNano()
	for k := range rw.seen {
		if k.created < floor {
			delete(rw.seen, k)
		}
	}
}

func (rw *rootWatch) flush(ctx context.Context, force bool) {
	if rw.store == nil || !rw.dirty {
		return
	}
	now := rw.now()
	if !force && now.Sub(rw.lastSave) < saveEvery {
		return
	}
	rw.lastSave = now
	raw, err := marshalState(rw.sentinel, recentFrom(rw.seen, rw.sentinel), pruneDone(rw.done, now.Add(-rw.cfg.SettleTimeout)))
	if err == nil {
		err = rw.store.Set(ctx, rw.key, raw)
	}
	if err != nil {
		if !rw.setWarned {
			rw.setWarned = true
			rw.logger.Warn("storage write failed; the sentinel stays in memory", zap.String("root", rw.root), zap.Error(err))
		}
		return
	}
	rw.dirty, rw.setWarned = false, false
}

// handle takes one watch message: events first, then the overflow it may carry.
func (rw *rootWatch) handle(ctx context.Context, msg watchMsg) {
	if len(msg.events) > 0 {
		rw.onEvents(ctx, msg.events)
	}
	if msg.overflow {
		add(ctx, rw.tel.overflows, 1)
		rw.logger.Warn("watch buffer overflow; rescanning the tree", zap.String("root", rw.root))
		rw.frozen, rw.rescanDue = true, true
		rw.tryRescan(ctx, true)
	}
}

func (rw *rootWatch) onEvents(ctx context.Context, events []rawEvent) {
	var batch []candidate
	for _, ev := range events {
		switch ev.action {
		case actionRenamedOld:
			rw.lastOld = ev.name
		case actionRenamedNew:
			old := rw.lastOld
			rw.lastOld = ""
			if old != "" && rw.match.excluded(old) {
				batch = rw.consider(ctx, ev.name, batch)
			}
		case actionAdded:
			batch = rw.consider(ctx, ev.name, batch)
		}
	}
	if len(batch) > 0 {
		// A failed delivery was logged by send and left the debt flags set; a rescan recovers the batch.
		_ = rw.emit(ctx, batch)
	}
}

// consider turns one created or renamed-in name into candidates; a directory contributes its files.
func (rw *rootWatch) consider(ctx context.Context, rel string, batch []candidate) []candidate {
	if rw.match.excluded(rel) {
		rw.countExcluded(ctx)
		return batch
	}
	path := filepath.Join(rw.root, rel)
	fi, err := rw.tree.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		add(ctx, rw.tel.vanished, 1)
		return batch
	case err != nil:
		rw.logger.Debug("file cannot be read; reporting it without a size", zap.String("path", path), zap.Error(err))
		return append(batch, candidate{path: path, size: -1, sizeFinal: -1})
	case fi.isDir:
		return rw.walkNew(ctx, path, batch)
	}
	return append(batch, candidate{path: path, size: fi.size, created: fi.created, modified: fi.modified, sizeFinal: -1})
}

// countExcluded counts one file or directory left out by an exclude pattern.
func (rw *rootWatch) countExcluded(ctx context.Context) {
	add(ctx, rw.tel.excluded, 1, attribute.String("root", rw.name))
}

// prune is the skipDir function of a walk that starts at base: it prunes an excluded directory and
// counts it once, so the files below it are never read or counted one by one.
func (rw *rootWatch) prune(ctx context.Context, base string) func(string) bool {
	return func(rel string) bool {
		if !rw.match.excluded(rw.rel(filepath.Join(base, rel))) {
			return false
		}
		rw.countExcluded(ctx)
		return true
	}
}

func (rw *rootWatch) walkNew(ctx context.Context, dir string, batch []candidate) []candidate {
	stats, err := rw.tree.Walk(ctx, dir, rw.prune(ctx, dir), func(f fileInfo) {
		if rw.match.excluded(rw.rel(f.path)) {
			rw.countExcluded(ctx)
			return
		}
		batch = append(batch, candidate{path: f.path, size: f.size, created: f.created, modified: f.modified, sizeFinal: -1})
	})
	if err != nil {
		rw.rescanDue = true
		rw.logger.Warn("could not walk a new directory; a rescan follows", zap.String("dir", dir), zap.Error(err))
	}
	rw.tel.skipped(ctx, "denied", int64(stats.denied))
	return batch
}

// tick runs the timed work: settling files, owed rescans, the optional interval scan, and the state write.
func (rw *rootWatch) tick(ctx context.Context) {
	rw.settleTick(ctx)
	switch {
	case rw.rescanDue:
		rw.tryRescan(ctx, true)
	case rw.cfg.RescanInterval > 0 && rw.now().Sub(rw.lastScan) >= rw.cfg.RescanInterval:
		rw.tryRescan(ctx, false)
	}
	rw.flush(ctx, false)
}

// settleTick checks every watched file. One whose size stayed put for settle gets a write-complete
// record with the final size; one still changing at settle_timeout gets the last size seen and is
// counted; one that is gone is dropped.
func (rw *rootWatch) settleTick(ctx context.Context) {
	if len(rw.pending) == 0 {
		return
	}
	now := rw.now()
	var ready []candidate
	for k, p := range rw.pending {
		fi, err := rw.tree.Stat(p.c.path)
		if errors.Is(err, fs.ErrNotExist) {
			delete(rw.pending, k)
			add(ctx, rw.tel.vanished, 1)
			continue
		}
		if err == nil && fi.size != p.lastSize {
			p.lastSize, p.lastChange = fi.size, now
		}
		switch {
		case err == nil && now.Sub(p.lastChange) >= rw.cfg.Settle:
			c := p.c
			c.size, c.sizeFinal, c.complete = p.lastSize, p.lastSize, true
			ready = append(ready, c)
		case now.Sub(p.firstSeen) >= rw.cfg.SettleTimeout:
			c := p.c
			c.size, c.complete = p.lastSize, true
			ready = append(ready, c)
		}
	}
	sortCandidates(ready)
	rw.complete(ctx, ready)
}

// complete sends write-complete records oldest first. A failed batch stays watched and is tried again
// at the next tick; it does not hold the sentinel, because the creation record is already out.
func (rw *rootWatch) complete(ctx context.Context, ready []candidate) {
	for len(ready) > 0 {
		n := min(len(ready), batchSize)
		now := rw.now()
		if err := rw.next.ConsumeLogs(ctx, rw.buildLogs(ready[:n], now)); err != nil {
			rw.warnDelivery(err, now, "write-complete delivery failed; trying again at the next check")
			rw.noteFailedCompletes(ctx, ready[:n], now)
			return
		}
		var timedOut int64
		for _, c := range ready[:n] {
			delete(rw.pending, strings.ToLower(c.path))
			rw.markDone(c)
			if c.sizeFinal < 0 {
				timedOut++
			}
		}
		add(ctx, rw.tel.completed, int64(n))
		add(ctx, rw.tel.timeouts, timedOut)
		ready = ready[n:]
	}
}

// markDone remembers a file whose last record is out, but only when a restart could read it back.
func (rw *rootWatch) markDone(c candidate) {
	if rw.store == nil {
		return
	}
	if k, ok := rw.keyOf(c); ok {
		rw.done[k] = struct{}{}
		rw.dirty = true
	}
}

// maxCompleteAttempts is how many times a write-complete record is sent before it is dropped.
const maxCompleteAttempts = 10

// noteFailedCompletes counts a failed attempt for each file and drops those that have used up
// maxCompleteAttempts, or have failed for settle_timeout. A drop is counted and logged once.
func (rw *rootWatch) noteFailedCompletes(ctx context.Context, batch []candidate, now time.Time) {
	var dropped int64
	for _, c := range batch {
		key := strings.ToLower(c.path)
		p, ok := rw.pending[key]
		if !ok {
			continue
		}
		if p.failedSince.IsZero() {
			p.failedSince = now
		}
		p.attempts++
		if p.attempts >= maxCompleteAttempts || now.Sub(p.failedSince) >= rw.cfg.SettleTimeout {
			delete(rw.pending, key)
			dropped++
		}
	}
	if dropped > 0 {
		add(ctx, rw.tel.dropped, dropped)
		rw.logger.Warn("gave up sending write-complete records", zap.String("root", rw.root), zap.Int64("files", dropped))
	}
}

func sortCandidates(cands []candidate) {
	sort.Slice(cands, func(i, j int) bool {
		if !cands[i].created.Equal(cands[j].created) {
			return cands[i].created.Before(cands[j].created)
		}
		return cands[i].path < cands[j].path
	})
}

// tryRescan scans for what a loss may have hidden. A limited rescan waits a minute after the last one
// and stops at max_rescans an hour.
func (rw *rootWatch) tryRescan(ctx context.Context, limited bool) {
	now := rw.now()
	if limited {
		if !rw.lastRescan.IsZero() && now.Sub(rw.lastRescan) < minRescanGap {
			return
		}
		rw.pruneQuota(now)
		if len(rw.rescans) >= rw.cfg.MaxRescans {
			if rw.cappedWarn.IsZero() || now.Sub(rw.cappedWarn) >= quotaWindow {
				rw.cappedWarn = now
				add(ctx, rw.tel.capped, 1)
				rw.logger.Warn("rescan limit reached; the sentinel stays held",
					zap.String("root", rw.root), zap.Int("max_rescans", rw.cfg.MaxRescans))
			}
			return
		}
		rw.rescans = append(rw.rescans, now)
	}
	rw.lastRescan = now
	add(ctx, rw.tel.rescans, 1)
	if err := rw.catchUp(ctx); err != nil {
		if ctx.Err() == nil {
			rw.logger.Warn("rescan did not finish; trying again later", zap.String("root", rw.root), zap.Error(err))
		}
		return
	}
	rw.frozen, rw.rescanDue = false, false
	rw.advance()
}

func (rw *rootWatch) pruneQuota(now time.Time) {
	keep := rw.rescans[:0]
	for _, t := range rw.rescans {
		if now.Sub(t) < quotaWindow {
			keep = append(keep, t)
		}
	}
	rw.rescans = keep
}

// nextDelay is how long the run loop may sleep; zero means nothing is timed.
func (rw *rootWatch) nextDelay() time.Duration {
	var d time.Duration
	use := func(x time.Duration) {
		if x > 0 && (d == 0 || x < d) {
			d = x
		}
	}
	now := rw.now()
	if len(rw.pending) > 0 {
		use(max(rw.cfg.Settle/4, minSettleGap))
	}
	if rw.dirty && rw.store != nil {
		use(max(saveEvery-now.Sub(rw.lastSave), 10*time.Millisecond))
	}
	if rw.rescanDue {
		use(debtPoll)
	}
	if rw.cfg.RescanInterval > 0 {
		use(max(rw.cfg.RescanInterval-now.Sub(rw.lastScan), time.Second))
	}
	return d
}

// close writes the state. Files still watched for growth get no write-complete record.
func (rw *rootWatch) close(ctx context.Context) {
	rw.flush(ctx, true)
}

// run is the live loop: open the watch, scan, then serve messages and timers until ctx ends.
func (rw *rootWatch) run(ctx context.Context) {
	rw.begin(ctx)
	defer func() {
		if rw.w != nil {
			_ = rw.w.Close()
		}
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rw.close(flushCtx)
	}()
	if !rw.connect(ctx) {
		return
	}
	for {
		timer := time.NewTimer(timerDelay(rw.nextDelay()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case msg, ok := <-rw.w.Messages():
			timer.Stop()
			if !ok || msg.err != nil {
				rw.logger.Warn("directory watch ended; reopening", zap.String("root", rw.root), zap.Error(msg.err))
				_ = rw.w.Close()
				rw.w = nil
				if !rw.connect(ctx) {
					return
				}
				continue
			}
			rw.handle(ctx, msg)
		case <-timer.C:
			rw.tick(ctx)
		}
	}
}

func timerDelay(d time.Duration) time.Duration {
	if d == 0 {
		return 24 * time.Hour
	}
	return d
}

// connect opens the watch, retrying with backoff while the root is missing or unreadable, then scans.
func (rw *rootWatch) connect(ctx context.Context) bool {
	delay := rw.backoffMin
	for {
		w, err := rw.open(rw.root, rw.cfg.BufferSize)
		if err == nil {
			rw.w = w
			if rw.openDown {
				rw.logger.Info("root is available again", zap.String("root", rw.root))
			}
			rw.openDown, rw.openErrMsg = false, ""
			if err := rw.catchUp(ctx); err != nil && ctx.Err() == nil {
				rw.logger.Warn("scan after opening the root did not finish; a rescan follows", zap.String("root", rw.root), zap.Error(err))
			}
			return true
		}
		if !rw.openDown || err.Error() != rw.openErrMsg {
			rw.openDown, rw.openErrMsg = true, err.Error()
			rw.logger.Warn("root cannot be watched; retrying", zap.String("root", rw.root), zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
		delay = min(delay*2, rw.backoffMax)
	}
}
