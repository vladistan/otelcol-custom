// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ksmreceiver

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/ksmreceiver"

// ksmFiles are the only files the receiver opens, each one with the metric it gives.
var ksmFiles = []struct {
	file, metric, unit, description string
	sum                             bool
}{
	{"pages_shared", "linux.ksm.stats.pages_shared", "{page}", "Shared pages in use.", false},
	{"pages_sharing", "linux.ksm.stats.pages_sharing", "{page}", "Sites that share a page, so the saving is this less pages_shared.", false},
	{"pages_unshared", "linux.ksm.stats.pages_unshared", "{page}", "Pages checked and not merged because no other page matches.", false},
	{"pages_volatile", "linux.ksm.stats.pages_volatile", "{page}", "Pages that change too fast to be merged.", false},
	{"full_scans", "linux.ksm.stats.full_scans", "{scan}", "Times KSM has scanned all candidate memory.", true},
	{"stable_node_chains", "linux.ksm.stats.stable_node_chains", "{node}", "Chains of stable nodes; a kernel without the file gives no series.", false},
	{"stable_node_dups", "linux.ksm.stats.stable_node_dups", "{node}", "Duplicate stable nodes in those chains; a kernel without the file gives no series.", false},
}

// unreadableEvery is how many scrapes apart the warning repeats while the directory exists and no
// file in it can be read.
const unreadableEvery = 60

var errNotDir = errors.New("not a directory")

// source reads the KSM files.
type source interface {
	check() error // nil for a directory; fs.ErrNotExist when it is missing
	read(name string) (string, error)
}

// fileSource reads the files of one directory; open and stat are replaced in tests.
type fileSource struct {
	dir  string
	open func(path string) ([]byte, error)
	stat func(path string) (fs.FileInfo, error)
}

func (f *fileSource) check() error {
	info, err := f.stat(f.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errNotDir
	}
	return nil
}

func (f *fileSource) read(name string) (string, error) {
	body, err := f.open(filepath.Join(f.dir, name))
	return string(body), err
}

// scraper turns the KSM files into metrics; it is used from one goroutine only.
type scraper struct {
	logger         *zap.Logger
	src            source
	start          time.Time // start time of the cumulative sum
	lastSum        uint64
	hasSum         bool
	hasGap         bool            // full_scans was not read since the last good read: a restart may be hidden
	hasMissingNote bool            // the info line for a missing directory has been written
	hasDirWarning  bool            // the warning for an unusable directory has been written
	failed         int             // scrapes in a row where files failed to read and none could be
	warned         map[string]bool // files with an open warning
}

func newScraper(logger *zap.Logger, cfg *Config) *scraper {
	return &scraper{
		logger: logger,
		src:    &fileSource{dir: cfg.KsmPath, open: os.ReadFile, stat: os.Stat},
		start:  time.Now(),
		warned: map[string]bool{},
	}
}

// scrape returns no resource when the directory is missing or no file could be read.
func (s *scraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	if err := ctx.Err(); err != nil {
		return pmetric.NewMetrics(), err
	}
	if err := s.src.check(); err != nil {
		s.hasGap = true
		s.dirProblem(err)
		return pmetric.NewMetrics(), nil
	}
	s.hasMissingNote, s.hasDirWarning = false, false

	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	now := pcommon.NewTimestampFromTime(time.Now())
	failures := 0
	for _, f := range ksmFiles {
		v, ok, failed := s.value(f.file)
		if failed {
			failures++
		}
		if !ok {
			if f.sum {
				s.hasGap = true
			}
			continue
		}
		m := sm.Metrics().AppendEmpty()
		m.SetName(f.metric)
		m.SetUnit(f.unit)
		m.SetDescription(f.description)
		var dp pmetric.NumberDataPoint
		if f.sum {
			sum := m.SetEmptySum()
			sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
			sum.SetIsMonotonic(true)
			dp = sum.DataPoints().AppendEmpty()
			dp.SetStartTimestamp(s.sumStart(v, now))
		} else {
			dp = m.SetEmptyGauge().DataPoints().AppendEmpty()
		}
		dp.SetTimestamp(now)
		dp.SetIntValue(int64(min(v, math.MaxInt64)))
	}
	if sm.Metrics().Len() == 0 {
		s.noneRead(failures)
		return pmetric.NewMetrics(), nil
	}
	s.failed = 0
	return md, nil
}

// dirProblem logs a missing directory once at info level, and any other failure of the directory
// (no access to a parent, a path that is a file) once as a warning, until the directory is usable.
func (s *scraper) dirProblem(err error) {
	if errors.Is(err, fs.ErrNotExist) {
		s.hasDirWarning = false
		if !s.hasMissingNote {
			s.hasMissingNote = true
			s.logger.Info("KSM directory not found, no KSM metrics until it exists")
		}
		return
	}
	s.hasMissingNote = false
	if !s.hasDirWarning {
		s.hasDirWarning = true
		s.logger.Warn("The KSM path is not a directory or cannot be read, so there are no KSM metrics",
			zap.String("path", s.dirPath()), zap.Error(err))
	}
}

func (s *scraper) dirPath() string {
	if f, ok := s.src.(*fileSource); ok {
		return f.dir
	}
	return ""
}

// noneRead warns that the directory exists and every file failed to read, on the first scrape of
// such a run and then every unreadableEvery scrapes. A directory of missing files is not a failure.
func (s *scraper) noneRead(failures int) {
	if failures == 0 {
		s.failed = 0
		return
	}
	if s.failed%unreadableEvery == 0 {
		s.logger.Warn("None of the KSM files could be read", zap.Int("files", failures))
	}
	s.failed++
}

// sumStart returns the start time of full_scans: the receiver start, moved to the scrape time
// when the counter goes down or when it was not read since the last good read, which are the two
// ways a restart of KSM can show. A restart that climbed back past the old value inside one gap is
// caught only by the gap.
func (s *scraper) sumStart(v uint64, now pcommon.Timestamp) pcommon.Timestamp {
	if s.hasSum && (v < s.lastSum || s.hasGap) {
		s.start = now.AsTime()
	}
	s.lastSum, s.hasSum, s.hasGap = v, true, false
	return pcommon.NewTimestampFromTime(s.start)
}

// value reads one file. A missing file gives nothing and no warning; a file that cannot be read or
// is not a number gives nothing, reports failed, and logs one warning per file until it reads
// cleanly again.
func (s *scraper) value(name string) (v uint64, ok, failed bool) {
	text, err := s.src.read(name)
	if errors.Is(err, fs.ErrNotExist) {
		delete(s.warned, name)
		return 0, false, false
	}
	if err == nil {
		if v, err = strconv.ParseUint(strings.TrimSpace(text), 10, 64); err == nil {
			delete(s.warned, name)
			return v, true, false
		}
	}
	if !s.warned[name] {
		s.warned[name] = true
		s.logger.Warn("Cannot read a KSM file", zap.String("file", name), zap.Error(err))
	}
	return 0, false, true
}
