// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/receiver/zfskstatreceiver"
	// maxKstatFile bounds one read; a pool io file is a few hundred bytes.
	maxKstatFile = 1 << 20
	arcKey       = "\x00arcstats"
	uptimePath   = "/proc/uptime"
	// maxUptime keeps a corrupt uptime from overflowing the duration.
	maxUptime = 50 * 365 * 24 * time.Hour
)

// ioPoint maps one io-file field to a data point; attr is empty when the point has no extra attribute.
type ioPoint struct {
	field, attr, value string
	scale              float64
}

// ioMetric is one pool metric built from io-file fields.
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
	name  string
	io    map[string]uint64
	state string
}

// scraper reads the kstat files; it is used from one goroutine only.
type scraper struct {
	logger  *zap.Logger
	cfg     *Config
	started time.Time // receiver start
	start   time.Time // start time of the cumulative sums
	warned  map[string]bool
	absent  bool
}

// newScraper starts the cumulative sums at host boot, or at the receiver start when boot time is unknown.
func newScraper(logger *zap.Logger, cfg *Config) *scraper {
	now := time.Now()
	return &scraper{
		logger:  logger,
		cfg:     cfg,
		started: now,
		start:   bootTime(uptimePath, now, now),
		warned:  map[string]bool{},
	}
}

// bootTime is now minus the uptime in the first field of the file; fallback when it cannot be read.
func bootTime(path string, now, fallback time.Time) time.Time {
	body, err := readCapped(path)
	if err != nil {
		return fallback
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return fallback
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || !(uptime >= 0) || uptime > maxUptime.Seconds() {
		return fallback
	}
	return now.Add(-time.Duration(uptime * float64(time.Second)))
}

// scrape returns no resource when there is nothing to report, so a host without ZFS sends nothing.
func (s *scraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	if err := ctx.Err(); err != nil {
		return pmetric.NewMetrics(), err
	}
	names, err := s.poolNames()
	if err != nil {
		return pmetric.NewMetrics(), err
	}
	var pools []poolStats
	for _, name := range names {
		if p, ok := s.readPool(name); ok {
			pools = append(pools, p)
		}
	}
	var arc map[string]uint64
	if s.cfg.ArcStats {
		arc = s.readArc()
	}

	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	now := pcommon.NewTimestampFromTime(time.Now())
	start := pcommon.NewTimestampFromTime(s.start)
	emitIO(sm, pools, start, now)
	emitState(sm, pools, now)
	emitArc(sm, arc, start, now)
	if sm.Metrics().Len() == 0 {
		return pmetric.NewMetrics(), nil
	}
	return md, nil
}

// poolNames lists the selected pool directories; a missing kstat_path is not an error.
func (s *scraper) poolNames() ([]string, error) {
	entries, err := os.ReadDir(s.cfg.KstatPath)
	if errors.Is(err, fs.ErrNotExist) {
		if !s.absent {
			s.absent = true
			s.logger.Info("ZFS kstat path not found, no ZFS metrics until it exists",
				zap.String("kstat_path", s.cfg.KstatPath))
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.cfg.KstatPath, err)
	}
	s.absent = false
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || slices.Contains(s.cfg.ExcludePools, name) {
			continue
		}
		if len(s.cfg.Pools) > 0 && !slices.Contains(s.cfg.Pools, name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// readPool reads the io and state files of one pool; the pool is skipped when its io file is missing or bad.
func (s *scraper) readPool(name string) (poolStats, bool) {
	dir := filepath.Join(s.cfg.KstatPath, name)
	fields, err := readIO(filepath.Join(dir, "io"))
	if errors.Is(err, fs.ErrNotExist) {
		s.logger.Debug("ZFS pool has no io file, skipping", zap.String("pool", name))
		return poolStats{}, false
	}
	if err != nil {
		s.warnOnce(name, "Skipping ZFS pool with an unreadable io file", zap.String("pool", name), zap.Error(err))
		return poolStats{}, false
	}
	delete(s.warned, name)

	p := poolStats{name: name, io: fields}
	body, err := readCapped(filepath.Join(dir, "state"))
	if err != nil {
		s.logger.Debug("ZFS pool state not readable", zap.String("pool", name), zap.Error(err))
		return p, true
	}
	p.state = strings.TrimSpace(string(body))
	return p, true
}

// readArc returns the numeric rows of the arcstats file; nil when it cannot be read.
func (s *scraper) readArc() map[string]uint64 {
	body, err := readCapped(filepath.Join(s.cfg.KstatPath, "arcstats"))
	if errors.Is(err, fs.ErrNotExist) {
		s.logger.Debug("ZFS arcstats file not found")
		return nil
	}
	if err != nil {
		s.warnOnce(arcKey, "Skipping ZFS ARC metrics, arcstats not readable", zap.Error(err))
		return nil
	}
	delete(s.warned, arcKey)

	stats := map[string]uint64{}
	lines := strings.Split(string(body), "\n")
	for _, line := range lines[min(2, len(lines)):] {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		if v, err := strconv.ParseUint(f[2], 10, 64); err == nil {
			stats[f[0]] = v
		}
	}
	return stats
}

// warnOnce logs a problem once until the same key reads cleanly again.
func (s *scraper) warnOnce(key, msg string, fields ...zap.Field) {
	if s.warned[key] {
		return
	}
	s.warned[key] = true
	s.logger.Warn(msg, fields...)
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxKstatFile))
}

func readIO(path string) (map[string]uint64, error) {
	body, err := readCapped(path)
	if err != nil {
		return nil, err
	}
	return parseIO(string(body))
}

// parseIO reads an io kstat: a header line, a line of field names and a line of values.
// Fields are matched by name, so a column added by a ZFS release does not shift the rest.
func parseIO(text string) (map[string]uint64, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 3 {
		return nil, errors.New("io file has fewer than three lines")
	}
	names, values := strings.Fields(lines[1]), strings.Fields(lines[2])
	if len(names) == 0 || len(values) < len(names) {
		return nil, fmt.Errorf("io file has %d names and %d values", len(names), len(values))
	}
	fields := make(map[string]uint64, len(names))
	for i, name := range names {
		v, err := strconv.ParseUint(values[i], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("io field %s: %w", name, err)
		}
		fields[name] = v
	}
	return fields, nil
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

func emitIO(sm pmetric.ScopeMetrics, pools []poolStats, start, now pcommon.Timestamp) {
	for _, m := range ioMetrics {
		var dps pmetric.NumberDataPointSlice
		created := false
		for _, p := range pools {
			for _, pt := range m.points {
				v, ok := p.io[pt.field]
				if !ok {
					continue
				}
				if !created {
					dps, created = newMetric(sm, m.name, m.unit, m.description, m.gauge), true
				}
				dp := dps.AppendEmpty()
				dp.Attributes().PutStr("zfs.pool", p.name)
				if pt.attr != "" {
					dp.Attributes().PutStr(pt.attr, pt.value)
				}
				if !m.gauge {
					dp.SetStartTimestamp(start)
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
