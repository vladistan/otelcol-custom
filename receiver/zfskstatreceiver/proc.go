// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// maxKstatFile bounds one read; a pool io file is a few hundred bytes.
	maxKstatFile = 1 << 20
	arcKey       = "\x00arcstats"
	uptimePath   = "/proc/uptime"
	// maxUptime keeps a corrupt uptime from overflowing the duration.
	maxUptime = 50 * 365 * 24 * time.Hour
	// objsetPrefix starts the name of a per-dataset kstat file.
	objsetPrefix = "objset-"
)

// procSource reads the OpenZFS kstat files under kstat_path.
type procSource struct {
	logger *zap.Logger
	cfg    *Config
	ep     *episodes
	absent bool
}

func newProcSource(logger *zap.Logger, cfg *Config) *procSource {
	return &procSource{logger: logger, cfg: cfg, ep: newEpisodes(logger)}
}

// bootTime is the host boot read from the uptime file, or now when it cannot be read.
func (p *procSource) bootTime(now time.Time) time.Time {
	return bootTime(uptimePath, now, now)
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

func (p *procSource) read() (snapshot, error) {
	names, err := p.poolNames()
	if err != nil {
		return snapshot{}, err
	}
	var snap snapshot
	for _, name := range names {
		if pool, ok := p.readPool(name); ok {
			snap.pools = append(snap.pools, pool)
		}
	}
	if p.cfg.ArcStats {
		snap.arc = p.readArc()
	}
	return snap, nil
}

// poolNames lists the selected pool directories; a missing kstat_path is not an error.
func (p *procSource) poolNames() ([]string, error) {
	entries, err := os.ReadDir(p.cfg.KstatPath)
	if errors.Is(err, fs.ErrNotExist) {
		if !p.absent {
			p.absent = true
			p.logger.Info("ZFS kstat path not found, no ZFS metrics until it exists",
				zap.String("kstat_path", p.cfg.KstatPath))
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.cfg.KstatPath, err)
	}
	p.absent = false
	var names []string
	for _, e := range entries {
		if e.IsDir() && p.cfg.selected(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// readPool reads the io, state and objset files of one pool. OpenZFS 2.1 and later have no io
// file, so a missing one gives a pool with no io fields; an io file that is bad skips the pool.
func (p *procSource) readPool(name string) (poolStats, bool) {
	dir := filepath.Join(p.cfg.KstatPath, name)
	fields, err := readIO(filepath.Join(dir, "io"))
	if errors.Is(err, fs.ErrNotExist) {
		p.logger.Debug("ZFS pool has no io file, as on OpenZFS 2.1 and later", zap.String("pool", name))
		fields, err = nil, nil
	}
	if err != nil {
		p.ep.warn(name, "Skipping ZFS pool with an unreadable io file", zap.String("pool", name), zap.Error(err))
		return poolStats{}, false
	}
	p.ep.clear(name)

	pool := poolStats{name: name, io: fields}
	if p.cfg.Datasets {
		pool.datasets = p.readDatasets(name, dir)
	}
	if p.cfg.Txg {
		pool.txg = p.readTxg(name, dir)
	}
	body, err := readCapped(filepath.Join(dir, "state"))
	if err != nil {
		p.logger.Debug("ZFS pool state not readable", zap.String("pool", name), zap.Error(err))
		return pool, true
	}
	pool.state = strings.TrimSpace(string(body))
	return pool, true
}

// readTxg returns the newest closed row of the txgs file; nil, with one info line per pool, when there is none.
func (p *procSource) readTxg(pool, dir string) *txgRow {
	key := "txg\x00" + pool
	body, err := readCapped(filepath.Join(dir, "txgs"))
	if err != nil {
		p.ep.once(zapcore.InfoLevel, key, "ZFS pool has no readable txgs file, so no txg metrics", zap.String("pool", pool), zap.Error(err))
		return nil
	}
	row, ok := parseTxgs(string(body))
	if !ok {
		p.ep.once(zapcore.InfoLevel, key, "ZFS pool txgs has no closed transaction group, so no txg metrics; zfs_txg_history may be 0", zap.String("pool", pool))
		return nil
	}
	p.ep.clear(key)
	return &row
}

// txgClosed is the state letter of a committed transaction group.
const txgClosed = "C"

// parseTxgs reads a txgs kstat: kstat header lines, a line of column names that starts with
// txg, then one row per transaction group. It returns the closed row with the highest txg.
// Columns are matched by name; the sync time is in nanoseconds.
func parseTxgs(text string) (txgRow, bool) {
	var cols map[string]int
	var best txgRow
	var bestTxg uint64
	found := false
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if cols == nil {
			if f[0] == "txg" {
				cols = make(map[string]int, len(f))
				for i, n := range f {
					cols[n] = i
				}
			}
			continue
		}
		row, txg, ok := txgFromFields(cols, f)
		if ok && (!found || txg > bestTxg) {
			best, bestTxg, found = row, txg, true
		}
	}
	return best, found
}

// txgFromFields returns the row when it is closed and its numbers parse.
func txgFromFields(cols map[string]int, f []string) (txgRow, uint64, bool) {
	get := func(name string) (string, bool) {
		i, ok := cols[name]
		if !ok || i >= len(f) {
			return "", false
		}
		return f[i], true
	}
	if state, ok := get("state"); !ok || state != txgClosed {
		return txgRow{}, 0, false
	}
	var nums [4]uint64
	for i, name := range []string{"txg", "ndirty", "nwritten", "stime"} {
		v, ok := get(name)
		if !ok {
			return txgRow{}, 0, false
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return txgRow{}, 0, false
		}
		nums[i] = n
	}
	return txgRow{dirty: nums[1], written: nums[2], syncNanos: nums[3]}, nums[0], true
}

// readDatasets reads the objset-* files of a pool; a file that vanishes is skipped quietly.
func (p *procSource) readDatasets(pool, dir string) []datasetStats {
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.logger.Debug("ZFS pool directory not readable", zap.String("pool", pool), zap.Error(err))
		return nil
	}
	var out []datasetStats
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), objsetPrefix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := readCapped(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			p.ep.warn(path, "Skipping ZFS dataset with an unreadable objset file", zap.String("file", path), zap.Error(err))
			continue
		}
		p.ep.clear(path)
		if name, fields, ok := parseObjset(string(body)); ok && p.cfg.datasetSelected(name) {
			out = append(out, datasetStats{pool: pool, name: name, fields: fields})
		}
	}
	return out
}

// readArc returns the numeric rows of the arcstats file; nil when it cannot be read.
func (p *procSource) readArc() map[string]uint64 {
	body, err := readCapped(filepath.Join(p.cfg.KstatPath, "arcstats"))
	if errors.Is(err, fs.ErrNotExist) {
		p.logger.Debug("ZFS arcstats file not found")
		return nil
	}
	if err != nil {
		p.ep.warn(arcKey, "Skipping ZFS ARC metrics, arcstats not readable", zap.Error(err))
		return nil
	}
	p.ep.clear(arcKey)

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

// parseObjset reads an objset kstat: two header lines, then "name type value" rows.
// Rows are matched by name; the dataset is dropped when it has no dataset_name row.
// The value is the rest of the row, so a dataset name may hold spaces.
func parseObjset(text string) (string, map[string]uint64, bool) {
	var name string
	fields := map[string]uint64{}
	lines := strings.Split(text, "\n")
	for _, line := range lines[min(2, len(lines)):] {
		key, value, ok := splitObjsetRow(line)
		if !ok {
			continue
		}
		if key == datasetNameField {
			name = value
			continue
		}
		if v, err := strconv.ParseUint(value, 10, 64); err == nil {
			fields[key] = v
		}
	}
	return name, fields, name != ""
}

// splitObjsetRow returns the first and the third column of a row; the third column runs to the end of the line.
func splitObjsetRow(line string) (key, value string, ok bool) {
	rest := strings.TrimSpace(line)
	var cols [2]string
	for i := range cols {
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			return "", "", false
		}
		cols[i] = rest[:end]
		rest = strings.TrimLeft(rest[end:], " \t")
	}
	if rest == "" {
		return "", "", false
	}
	return cols[0], rest, true
}
