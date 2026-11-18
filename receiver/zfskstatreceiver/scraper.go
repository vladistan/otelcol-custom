// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/receiver/zfskstatreceiver"
	// datasetNameField is the objset kstat field that holds the dataset name.
	datasetNameField = "dataset_name"
)

// ioPoint maps one io-file field to a data point; attr is empty when the point has no extra attribute.
type ioPoint struct {
	field, attr, value string
	scale              float64
}

// ioMetric is one metric built from io-file fields.
type ioMetric struct {
	name, unit, description string
	gauge                   bool
	points                  []ioPoint
}

var ioMetrics = []ioMetric{
	{"zfs.pool.io.bytes", "By", "Bytes read from and written to the pool.", false, []ioPoint{
		{"nread", "direction", "read", 1}, {"nwritten", "direction", "write", 1},
	}},
	{"zfs.pool.io.operations", "{operation}", "Read and write operations on the pool.", false, []ioPoint{
		{"reads", "direction", "read", 1}, {"writes", "direction", "write", 1},
	}},
	{"zfs.pool.io.wait.busy.time", "s", "Time the pool I/O wait queue was not empty. A rate of this sum is the share of time the queue was busy, not a per-operation latency.", false, []ioPoint{
		{"wtime", "", "", 1e-9},
	}},
	{"zfs.pool.io.run.busy.time", "s", "Time the pool I/O run queue was not empty. A rate of this sum is the share of time the queue was busy, not a per-operation latency.", false, []ioPoint{
		{"rtime", "", "", 1e-9},
	}},
	{"zfs.pool.io.queue.length", "{operation}", "Operations now in the pool wait and run queues.", true, []ioPoint{
		{"wcnt", "queue", "wait", 1}, {"rcnt", "queue", "run", 1},
	}},
}

var datasetMetrics = []ioMetric{
	{"zfs.dataset.io.bytes", "By", "Bytes read from and written to the dataset.", false, []ioPoint{
		{"nread", "direction", "read", 1}, {"nwritten", "direction", "write", 1},
	}},
	{"zfs.dataset.io.operations", "{operation}", "Read and write operations on the dataset.", false, []ioPoint{
		{"reads", "direction", "read", 1}, {"writes", "direction", "write", 1},
	}},
	{"zfs.dataset.unlinks", "{unlink}", "Files and directories removed from the dataset.", false, []ioPoint{
		{"nunlinks", "", "", 1},
	}},
}

// arcMetric is one ARC metric read from the arcstats file by name.
type arcMetric struct {
	field, name, unit, description string
	gauge                          bool
}

var arcMetrics = []arcMetric{
	{"size", "zfs.arc.size", "By", "Current size of the ARC.", true},
	{"hits", "zfs.arc.hits", "{hit}", "ARC lookups that found the block.", false},
	{"misses", "zfs.arc.misses", "{miss}", "ARC lookups that did not find the block.", false},
}

type poolStats struct {
	name     string
	io       map[string]uint64
	state    string
	datasets []datasetStats
	txg      *txgRow     // newest closed transaction group; nil when there is none
	space    *spaceStats // FreeBSD only: statfs of the root dataset; nil when it is not mounted
}

// txgRow is the newest closed transaction group of a pool.
type txgRow struct {
	dirty, written, syncNanos uint64
}

// spaceStats is the statfs answer for the root dataset of a pool, in bytes.
type spaceStats struct {
	used, available uint64
}

type datasetStats struct {
	pool, name string
	fields     map[string]uint64
}

// snapshot is what one source read returns.
type snapshot struct {
	pools []poolStats
	arc   map[string]uint64
}

// kstatSource reads the ZFS statistics from one place.
type kstatSource interface {
	// read returns the selected pools and the ARC stats; a host without ZFS gives an empty snapshot.
	read() (snapshot, error)
	// bootTime is the host boot, or now when it cannot be read.
	bootTime(now time.Time) time.Time
}

// episodes logs a problem once until the same key reads cleanly again.
type episodes struct {
	logger *zap.Logger
	warned map[string]bool
}

func newEpisodes(logger *zap.Logger) *episodes {
	return &episodes{logger: logger, warned: map[string]bool{}}
}

func (e *episodes) warn(key, msg string, fields ...zap.Field) {
	e.once(zapcore.WarnLevel, key, msg, fields...)
}

// once logs msg at level the first time the key is seen since it last cleared.
func (e *episodes) once(level zapcore.Level, key, msg string, fields ...zap.Field) {
	if e.warned[key] {
		return
	}
	e.warned[key] = true
	if ce := e.logger.Check(level, msg); ce != nil {
		ce.Write(fields...)
	}
}

func (e *episodes) clear(key string) { delete(e.warned, key) }

// scraper turns source reads into metrics; it is used from one goroutine only.
type scraper struct {
	cfg      *Config
	source   kstatSource
	episodes *episodes
	resets   *resetTracker
	started  time.Time // receiver start
	start    time.Time // start time of the cumulative sums
	space    *spaceSource
}

// newScraper picks the source named by the config; auto means sysctl on FreeBSD and proc elsewhere.
// The space series are read when ioctl_device is set.
func newScraper(logger *zap.Logger, cfg *Config) *scraper {
	var s *scraper
	if cfg.resolvedSource() == sourceSysctl {
		s = newScraperWith(logger, cfg, newSysctlSource(logger, cfg, realSysctl()))
	} else {
		s = newScraperWith(logger, cfg, newProcSource(logger, cfg))
	}
	if cfg.IoctlDevice != "" {
		s.space = newSpaceSource(logger, cfg, openZFS)
	}
	return s
}

func (s *scraper) close() {
	if s.space != nil {
		s.space.close()
	}
}

// readSpace reads the ioctl space series for the pools of a snapshot; nil when there is nothing to read.
func (s *scraper) readSpace(pools []poolStats) *spaceResult {
	if s.space == nil || len(pools) == 0 {
		return nil
	}
	names := make([]string, len(pools))
	for i, p := range pools {
		names[i] = p.name
	}
	r, err := s.space.scan(names)
	if err != nil {
		s.episodes.warn("space-scan", "ZFS space series skipped for this scrape", zap.Error(err))
		return nil
	}
	s.episodes.clear("space-scan")
	return r
}

// dropCoveredStatfs removes the statfs root-dataset points of pools whose root dataset the ioctl read gave.
func dropCoveredStatfs(pools []poolStats, r *spaceResult) {
	if r == nil {
		return
	}
	read := map[string]bool{}
	for _, d := range r.datasets {
		read[d.name] = true
	}
	for i := range pools {
		if read[pools[i].name] {
			pools[i].space = nil
		}
	}
}

// newScraperWith starts the cumulative sums at host boot, or at the receiver start when boot time is unknown.
func newScraperWith(logger *zap.Logger, cfg *Config, source kstatSource) *scraper {
	now := time.Now()
	return &scraper{
		cfg: cfg, source: source, episodes: newEpisodes(logger), resets: newResetTracker(),
		started: now, start: source.bootTime(now),
	}
}

// resetTracker moves the start time of a cumulative series to the scrape time
// when its value goes down, which is how a remounted dataset or a re-imported
// pool shows up. A series absent from a scrape is forgotten.
type resetTracker struct {
	last, seen map[string]trackedPoint
}

type trackedPoint struct {
	value uint64
	start pcommon.Timestamp
}

func newResetTracker() *resetTracker {
	return &resetTracker{last: map[string]trackedPoint{}, seen: map[string]trackedPoint{}}
}

// startFor returns the start time of the series with this key.
func (t *resetTracker) startFor(key string, v uint64, boot, now pcommon.Timestamp) pcommon.Timestamp {
	start := boot
	if prev, ok := t.last[key]; ok {
		start = prev.start
		if v < prev.value {
			start = now
		}
	}
	t.seen[key] = trackedPoint{v, start}
	return start
}

// endScrape drops the series that this scrape did not see.
func (t *resetTracker) endScrape() {
	t.last, t.seen = t.seen, map[string]trackedPoint{}
}

const capKey = "max_datasets"

// capSorted sorts the datasets by name and then pool, and returns the first limit of them.
// It warns once per episode when it drops some; a limit of 0 or less keeps all.
func capSorted[T any](ep *episodes, limit int, all []T, id func(T) (pool, name string)) []T {
	sort.Slice(all, func(i, j int) bool {
		pi, ni := id(all[i])
		pj, nj := id(all[j])
		if ni != nj {
			return ni < nj
		}
		return pi < pj
	})
	if limit <= 0 || len(all) <= limit {
		ep.clear(capKey)
		return all
	}
	ep.warn(capKey, "ZFS datasets above max_datasets are not reported",
		zap.Int("datasets", len(all)), zap.Int("max_datasets", limit))
	return all[:limit]
}

// capDatasets keeps the first max datasets in sorted order across all pools.
func (s *scraper) capDatasets(pools []poolStats) {
	limit := s.cfg.MaxDatasets
	total := 0
	for _, p := range pools {
		total += len(p.datasets)
	}
	if limit <= 0 || total <= limit {
		s.episodes.clear(capKey)
		return
	}
	all := make([]datasetStats, 0, total)
	for _, p := range pools {
		all = append(all, p.datasets...)
	}
	kept := capSorted(s.episodes, limit, all, func(d datasetStats) (string, string) { return d.pool, d.name })
	keep := make(map[[2]string]bool, limit)
	for _, d := range kept {
		keep[[2]string{d.pool, d.name}] = true
	}
	for i := range pools {
		kept := pools[i].datasets[:0:0]
		for _, d := range pools[i].datasets {
			if keep[[2]string{d.pool, d.name}] {
				kept = append(kept, d)
			}
		}
		pools[i].datasets = kept
	}
}

// scrape returns no resource when there is nothing to report, so a host without ZFS sends nothing.
func (s *scraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	if err := ctx.Err(); err != nil {
		return pmetric.NewMetrics(), err
	}
	snap, err := s.source.read()
	if err != nil {
		return pmetric.NewMetrics(), err
	}

	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	now := pcommon.NewTimestampFromTime(time.Now())
	start := pcommon.NewTimestampFromTime(s.start)
	if s.cfg.Datasets {
		s.capDatasets(snap.pools)
	}
	emitIO(sm, ioMetrics, poolSeries(snap.pools), s.resets, start, now)
	emitState(sm, snap.pools, now)
	if s.cfg.Txg {
		emitTxg(sm, snap.pools, now)
	}
	sp := s.readSpace(snap.pools)
	dropCoveredStatfs(snap.pools, sp)
	emitSpace(sm, snap.pools, now)
	emitIoctlSpace(sm, sp, s.resets, start, now)
	if s.cfg.Datasets {
		emitIO(sm, datasetMetrics, datasetSeries(snap.pools), s.resets, start, now)
	}
	s.resets.endScrape()
	emitArc(sm, snap.arc, start, now)
	if sm.Metrics().Len() == 0 {
		return pmetric.NewMetrics(), nil
	}
	return md, nil
}

// series is one set of io fields and the attributes of its data points.
type series struct {
	attrs  [][2]string
	fields map[string]uint64
}

func poolSeries(pools []poolStats) []series {
	out := make([]series, 0, len(pools))
	for _, p := range pools {
		out = append(out, series{[][2]string{{"zfs.pool", p.name}}, p.io})
	}
	return out
}

func datasetSeries(pools []poolStats) []series {
	var out []series
	for _, p := range pools {
		for _, d := range p.datasets {
			out = append(out, series{[][2]string{{"zfs.pool", d.pool}, {"zfs.dataset", d.name}}, d.fields})
		}
	}
	return out
}

// seriesKey names one data point series: the metric, its attributes and the point's own attribute.
func seriesKey(metric string, attrs [][2]string, pt ioPoint) string {
	var b strings.Builder
	b.WriteString(metric)
	for _, a := range attrs {
		b.WriteString("\x00" + a[0] + "=" + a[1])
	}
	b.WriteString("\x00" + pt.attr + "=" + pt.value + "\x00" + pt.field)
	return b.String()
}

func newMetric(sm pmetric.ScopeMetrics, name, unit, description string, gauge bool) pmetric.NumberDataPointSlice {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	m.SetDescription(description)
	if gauge {
		return m.SetEmptyGauge().DataPoints()
	}
	sum := m.SetEmptySum()
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	sum.SetIsMonotonic(true)
	return sum.DataPoints()
}

// setValue writes a whole number as an int, clamped at the int64 limit, or a scaled value as a double.
func setValue(dp pmetric.NumberDataPoint, v uint64, scale float64) {
	if scale != 1 {
		dp.SetDoubleValue(float64(v) * scale)
		return
	}
	dp.SetIntValue(int64(min(v, math.MaxInt64)))
}

func emitIO(scopeMetrics pmetric.ScopeMetrics, metrics []ioMetric, all []series, tracker *resetTracker, boot, now pcommon.Timestamp) {
	for _, m := range metrics {
		var points pmetric.NumberDataPointSlice
		hasMetric := false
		for _, s := range all {
			for _, pt := range m.points {
				v, ok := s.fields[pt.field]
				if !ok {
					continue
				}
				if !hasMetric {
					points, hasMetric = newMetric(scopeMetrics, m.name, m.unit, m.description, m.gauge), true
				}
				dp := points.AppendEmpty()
				for _, a := range s.attrs {
					dp.Attributes().PutStr(a[0], a[1])
				}
				if pt.attr != "" {
					dp.Attributes().PutStr(pt.attr, pt.value)
				}
				if !m.gauge {
					dp.SetStartTimestamp(tracker.startFor(seriesKey(m.name, s.attrs, pt), v, boot, now))
				}
				dp.SetTimestamp(now)
				setValue(dp, v, pt.scale)
			}
		}
	}
}

func emitState(scopeMetrics pmetric.ScopeMetrics, pools []poolStats, now pcommon.Timestamp) {
	var points pmetric.NumberDataPointSlice
	hasMetric := false
	for _, p := range pools {
		if p.state == "" {
			continue
		}
		if !hasMetric {
			points, hasMetric = newMetric(scopeMetrics, "zfs.pool.state", "1", "Pool health as an info metric; the state is an attribute.", true), true
		}
		dp := points.AppendEmpty()
		dp.Attributes().PutStr("zfs.pool", p.name)
		dp.Attributes().PutStr("state", p.state)
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
	}
}

// txgMetrics are the gauges of the newest closed transaction group.
var txgMetrics = []struct {
	name, unit, description string
	value                   func(txgRow) float64
	isWhole                 bool
}{
	{"zfs.pool.txg.dirty", "By", "Dirty data in the newest closed transaction group.", func(r txgRow) float64 { return float64(r.dirty) }, true},
	{"zfs.pool.txg.written", "By", "Data written by the newest closed transaction group.", func(r txgRow) float64 { return float64(r.written) }, true},
	{"zfs.pool.txg.sync.time", "s", "Time the newest closed transaction group took to sync.", func(r txgRow) float64 { return float64(r.syncNanos) * 1e-9 }, false},
}

func emitTxg(scopeMetrics pmetric.ScopeMetrics, pools []poolStats, now pcommon.Timestamp) {
	for _, m := range txgMetrics {
		var points pmetric.NumberDataPointSlice
		hasMetric := false
		for _, p := range pools {
			if p.txg == nil {
				continue
			}
			if !hasMetric {
				points, hasMetric = newMetric(scopeMetrics, m.name, m.unit, m.description, true), true
			}
			dp := points.AppendEmpty()
			dp.Attributes().PutStr("zfs.pool", p.name)
			dp.SetTimestamp(now)
			if m.isWhole {
				setValue(dp, uint64(m.value(*p.txg)), 1)
			} else {
				dp.SetDoubleValue(m.value(*p.txg))
			}
		}
	}
}

// spaceMetrics are the FreeBSD gauges from statfs of the root dataset.
var spaceMetrics = []struct {
	name, description string
	value             func(spaceStats) uint64
}{
	{"zfs.dataset.space.used", "Space the root dataset of the pool uses, from statfs. Not the pool ALLOC of zpool list.", func(s spaceStats) uint64 { return s.used }},
	{"zfs.dataset.space.available", "Space the root dataset of the pool can still use, from statfs. Not the pool FREE of zpool list.", func(s spaceStats) uint64 { return s.available }},
}

func emitSpace(scopeMetrics pmetric.ScopeMetrics, pools []poolStats, now pcommon.Timestamp) {
	for _, m := range spaceMetrics {
		var points pmetric.NumberDataPointSlice
		hasMetric := false
		for _, p := range pools {
			if p.space == nil {
				continue
			}
			if !hasMetric {
				points, hasMetric = newMetric(scopeMetrics, m.name, "By", m.description, true), true
			}
			dp := points.AppendEmpty()
			dp.Attributes().PutStr("zfs.pool", p.name)
			dp.Attributes().PutStr("zfs.dataset", p.name)
			dp.SetTimestamp(now)
			setValue(dp, m.value(*p.space), 1)
		}
	}
}

func emitArc(sm pmetric.ScopeMetrics, arc map[string]uint64, start, now pcommon.Timestamp) {
	for _, m := range arcMetrics {
		v, ok := arc[m.field]
		if !ok {
			continue
		}
		dp := newMetric(sm, m.name, m.unit, m.description, m.gauge).AppendEmpty()
		if !m.gauge {
			dp.SetStartTimestamp(start)
		}
		dp.SetTimestamp(now)
		setValue(dp, v, 1)
	}
}
