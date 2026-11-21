// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var testRoot = filepath.FromSlash("/cds")

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fakeTree struct {
	mu      sync.Mutex
	files   map[string]fileInfo
	statErr map[string]error
	denied  int
	walks   int
}

func newTree() *fakeTree {
	return &fakeTree{files: map[string]fileInfo{}, statErr: map[string]error{}}
}

func (f *fakeTree) put(rel string, size int64, created time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := filepath.Join(testRoot, rel)
	f.files[p] = fileInfo{path: p, size: size, created: created}
	return p
}

func (f *fakeTree) putModified(rel string, size int64, created, modified time.Time) string {
	p := f.put(rel, size, created)
	f.mu.Lock()
	defer f.mu.Unlock()
	fi := f.files[p]
	fi.modified = modified
	f.files[p] = fi
	return p
}

func (f *fakeTree) putDir(rel string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := filepath.Join(testRoot, rel)
	f.files[p] = fileInfo{path: p, isDir: true}
	return p
}

func (f *fakeTree) remove(rel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, filepath.Join(testRoot, rel))
}

func (f *fakeTree) Walk(_ context.Context, root string, skipDir func(string) bool, fn func(fileInfo)) (walkStats, error) {
	f.mu.Lock()
	var list []fileInfo
	for p, fi := range f.files {
		if !fi.isDir && strings.HasPrefix(p, root+string(filepath.Separator)) {
			list = append(list, fi)
		}
	}
	denied := f.denied
	f.walks++
	f.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].path < list[j].path })
	asked := map[string]bool{} // the real walk offers each directory to skipDir once
	for _, fi := range list {
		if pruned(root, fi.path, skipDir, asked) {
			continue
		}
		fn(fi)
	}
	return walkStats{denied: denied}, nil
}

// pruned reports whether skipDir prunes a directory above path, offering each directory once.
func pruned(root, path string, skipDir func(string) bool, asked map[string]bool) bool {
	if skipDir == nil {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || rel == "." {
		return false
	}
	dir := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		skip, seen := asked[dir]
		if !seen {
			skip = skipDir(dir)
			asked[dir] = skip
		}
		if skip {
			return true
		}
	}
	return false
}

func (f *fakeTree) walkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.walks
}

func (f *fakeTree) Stat(path string) (fileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.statErr[path]; ok {
		return fileInfo{}, err
	}
	fi, ok := f.files[path]
	if !ok {
		return fileInfo{}, fs.ErrNotExist
	}
	return fi, nil
}

type memStore struct {
	mu     sync.Mutex
	data   map[string][]byte
	getErr error
	setErr error
	sets   int
}

func newStore() *memStore { return &memStore{data: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.data[key], nil
}

func (m *memStore) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	m.sets++
	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memStore) saved(t *testing.T, root string) persisted {
	t.Helper()
	m.mu.Lock()
	raw := m.data[rootKey(root)]
	m.mu.Unlock()
	if raw == nil {
		t.Fatalf("nothing stored under %q", rootKey(root))
	}
	st, err := parseState(raw)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// flakySink refuses every call while fail is set, and every call from the failAt-th on when failAt is positive.
type flakySink struct {
	consumertest.LogsSink
	fail   atomic.Bool
	failAt atomic.Int32
	calls  atomic.Int32
}

func (s *flakySink) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	n := s.calls.Add(1)
	if at := s.failAt.Load(); s.fail.Load() || (at > 0 && n >= at) {
		return errors.New("downstream refused")
	}
	return s.LogsSink.ConsumeLogs(ctx, ld)
}

type record struct {
	body     string
	severity plog.SeverityNumber
	ts, obs  time.Time
	attrs    map[string]any
}

func records(sink *flakySink) []record {
	var out []record
	for _, ld := range sink.AllLogs() {
		for i := 0; i < ld.ResourceLogs().Len(); i++ {
			rl := ld.ResourceLogs().At(i)
			for j := 0; j < rl.ScopeLogs().Len(); j++ {
				sl := rl.ScopeLogs().At(j)
				for k := 0; k < sl.LogRecords().Len(); k++ {
					lr := sl.LogRecords().At(k)
					out = append(out, record{
						body:     lr.Body().AsString(),
						severity: lr.SeverityNumber(),
						ts:       lr.Timestamp().AsTime(),
						obs:      lr.ObservedTimestamp().AsTime(),
						attrs:    lr.Attributes().AsRaw(),
					})
				}
			}
		}
	}
	return out
}

func names(recs []record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.attrs["file.name"].(string))
	}
	return out
}

type rig struct {
	t     *testing.T
	rw    *rootWatch
	clock *fakeClock
	tree  *fakeTree
	store *memStore
	sink  *flakySink
	logs  *observer.ObservedLogs
	read  *sdkmetric.ManualReader
	cfg   *Config
}

type rigOption func(*rig)

func withConfig(fn func(*Config)) rigOption { return func(r *rig) { fn(r.cfg) } }

func newRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()
	r := &rig{t: t, clock: newClock(), tree: newTree(), store: newStore(), sink: &flakySink{}}
	r.cfg = createDefaultConfig().(*Config)
	r.cfg.Paths = []string{testRoot}
	r.cfg.Settle = 0 // tests of write-complete records turn it on
	r.cfg.Exclude = []string{`**/~*`, `**/*.tmp`}
	for _, o := range opts {
		o(r)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	r.logs = logs
	r.read = sdkmetric.NewManualReader()
	tel, err := newTelemetry(component.TelemetrySettings{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(r.read))})
	if err != nil {
		t.Fatal(err)
	}
	spec := r.cfg.rootSpecs()[0]
	m, err := newMatcher(spec.patterns())
	if err != nil {
		t.Fatal(err)
	}
	r.rw = newRootWatch(rootParams{
		cfg: r.cfg, root: testRoot, name: spec.name, logger: zap.New(core), next: r.sink, tel: tel,
		tree: r.tree, store: r.store, now: r.clock.Now, match: m,
	})
	return r
}

// start runs the same sequence as the run loop: state, first start, catch-up scan.
func (r *rig) start() {
	r.t.Helper()
	ctx := context.Background()
	r.rw.begin(ctx)
	if err := r.rw.catchUp(ctx); err != nil {
		r.t.Logf("catch-up: %v", err)
	}
}

func (r *rig) added(rel string) watchMsg {
	return watchMsg{events: []rawEvent{{action: actionAdded, name: rel}}}
}

func (r *rig) handle(msg watchMsg) { r.rw.handle(context.Background(), msg) }
func (r *rig) tick()               { r.rw.tick(context.Background()) }
func (r *rig) records() []record   { return records(r.sink) }

func (r *rig) counter(name string, attrs ...string) int64 {
	r.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.read.Collect(context.Background(), &rm); err != nil {
		r.t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			for _, dp := range sum.DataPoints {
				if matches(dp, attrs) {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func matches(dp metricdata.DataPoint[int64], pairs []string) bool {
	for i := 0; i+1 < len(pairs); i += 2 {
		v, ok := dp.Attributes.Value(attrKey(pairs[i]))
		if !ok || v.String() != pairs[i+1] {
			return false
		}
	}
	return true
}

func (r *rig) warnings(substr string) int {
	n := 0
	for _, e := range r.logs.All() {
		if e.Level == zapcore.WarnLevel && strings.Contains(e.Message, substr) {
			n++
		}
	}
	return n
}

func attrKey(s string) attribute.Key { return attribute.Key(s) }

func ago(c *fakeClock, d time.Duration) time.Time { return c.Now().Add(-d) }

// putAbs adds a file by absolute path, for roots other than testRoot.
func (f *fakeTree) putAbs(path string, size int64, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = fileInfo{path: path, size: size, created: created}
}
