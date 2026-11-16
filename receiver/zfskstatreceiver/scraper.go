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
	if e.warned[key] {
		return
	}
	e.warned[key] = true
	e.logger.Warn(msg, fields...)
}

func (e *episodes) clear(key string) { delete(e.warned, key) }

// scraper turns source reads into metrics; it is used from one goroutine only.
type scraper struct {
	cfg     *Config
	src     kstatSource
	ep      *episodes
	resets  *resetTracker
	started time.Time // receiver start
	start   time.Time // start time of the cumulative sums
}

// newScraper picks the source named by the config; auto means sysctl on FreeBSD and proc elsewhere.
func newScraper(logger *zap.Logger, cfg *Config) *scraper {
	if cfg.resolvedSource() == sourceSysctl {
		return newScraperWith(logger, cfg, newSysctlSource(logger, cfg, realSysctl()))
	}
	return newScraperWith(logger, cfg, newProcSource(logger, cfg))
}

// newScraperWith starts the cumulative sums at host boot, or at the receiver start when boot time is unknown.
func newScraperWith(logger *zap.Logger, cfg *Config, src kstatSource) *scraper {
	now := time.Now()
	return &scraper{
		cfg: cfg, src: src, ep: newEpisodes(logger), resets: newResetTracker(),
		started: now, start: src.bootTime(now),
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

// capDatasets keeps the first max datasets in sorted order and warns once per episode when it drops some.
func (s *scraper) capDatasets(pools []poolStats) {
	limit := s.cfg.MaxDatasets
	total := 0
	for _, p := range pools {
		total += len(p.datasets)
	}
	if limit <= 0 || total <= limit {
		s.ep.clear(capKey)
		return
	}
	all := make([]datasetStats, 0, total)
	for _, p := range pools {
		all = append(all, p.datasets...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].name != all[j].name {
			return all[i].name < all[j].name
		}
		return all[i].pool < all[j].pool
	})
	keep := make(map[[2]string]bool, limit)
	for _, d := range all[:limit] {
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
	s.ep.warn(capKey, "ZFS datasets above max_datasets are not reported",
		zap.Int("datasets", total), zap.Int("max_datasets", limit))
}

// scrape returns no resource when there is nothing to report, so a host without ZFS sends nothing.
func (s *scraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	if err := ctx.Err(); err != nil {
		return pmetric.NewMetrics(), err
	}
	snap, err := s.src.read()
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

func emitIO(sm pmetric.ScopeMetrics, metrics []ioMetric, all []series, tr *resetTracker, boot, now pcommon.Timestamp) {
	for _, m := range metrics {
		var dps pmetric.NumberDataPointSlice
		created := false
		for _, s := range all {
			for _, pt := range m.points {
				v, ok := s.fields[pt.field]
				if !ok {
					continue
				}
				if !created {
					dps, created = newMetric(sm, m.name, m.unit, m.description, m.gauge), true
				}
				dp := dps.AppendEmpty()
				for _, a := range s.attrs {
					dp.Attributes().PutStr(a[0], a[1])
				}
				if pt.attr != "" {
					dp.Attributes().PutStr(pt.attr, pt.value)
				}
				if !m.gauge {
					dp.SetStartTimestamp(tr.startFor(seriesKey(m.name, s.attrs, pt), v, boot, now))
				}
				dp.SetTimestamp(now)
				setValue(dp, v, pt.scale)
			}
		}
	}
}

func emitState(sm pmetric.ScopeMetrics, pools []poolStats, now pcommon.Timestamp) {
	var dps pmetric.NumberDataPointSlice
	created := false
	for _, p := range pools {
		if p.state == "" {
			continue
		}
		if !created {
			dps, created = newMetric(sm, "zfs.pool.state", "1", "Pool health as an info metric; the state is an attribute.", true), true
		}
		dp := dps.AppendEmpty()
		dp.Attributes().PutStr("zfs.pool", p.name)
		dp.Attributes().PutStr("state", p.state)
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
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
