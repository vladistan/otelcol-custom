package psireceiver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	scopeName    = "github.com/vladistan/otelcol-custom/receiver/psireceiver"
	scopeVersion = "0.1.0"
)

var pressureResources = []string{"cpu", "memory", "io"}

// cgroupScraper reads the pressure files of the cgroups the config selects.
type cgroupScraper struct {
	cfg     CgroupConfig
	logger  *zap.Logger
	skipped metric.Int64Counter // nil until useTelemetry
	start   pcommon.Timestamp

	// A warning that repeats every interval is logged once, then again after the condition clears.
	pathWarned        bool
	controllersWarned bool
	capWarned         bool
	listWarned        bool
}

type cgroupEntry struct {
	rel  string // slash-separated, relative to the root
	dir  string
	info cgroupPath
}

// pressureFile is one parsed <resource>.pressure file.
type pressureFile struct {
	data       psiData
	some, full bool
}

func newCgroupScraper(logger *zap.Logger, cfg CgroupConfig) (*cgroupScraper, error) {
	return &cgroupScraper{cfg: cfg, logger: logger, start: pcommon.NewTimestampFromTime(time.Now())}, nil
}

func (c *cgroupScraper) useTelemetry(ts component.TelemetrySettings) error {
	counter, err := ts.MeterProvider.Meter(scopeName).Int64Counter(
		"otelcol_psi_cgroups_skipped",
		metric.WithDescription("Total cgroups left out of scrapes because a scrape found more than max_cgroups; a cgroup over the cap is counted again at every scrape."),
		metric.WithUnit("{cgroup}"),
	)
	if err != nil {
		return fmt.Errorf("create the cgroups_skipped counter: %w", err)
	}
	c.skipped = counter
	return nil
}

// scrape appends the cgroup series to md. It never fails the scrape: problems are logged and the host series stay.
func (c *cgroupScraper) scrape(ctx context.Context, md pmetric.Metrics, now pcommon.Timestamp) {
	if !c.rootReady() {
		return
	}
	kept := c.selectCgroups(ctx)

	out := newCgroupBuilder(md, c.start, now)
	attempts, failures := 0, 0
	var firstErr error
	for _, entry := range kept {
		for _, resource := range pressureResources {
			attempts++
			file, err := c.readPressure(filepath.Join(entry.dir, resource+".pressure"))
			if err != nil {
				failures++
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if file == nil {
				continue
			}
			out.add(entry, resource, file, c.cfg.Values)
		}
	}
	switch {
	case attempts > 0 && failures == attempts:
		c.logger.Warn("no cgroup pressure file could be read in this interval",
			zap.String("path", c.cfg.Path), zap.Int("files", attempts), zap.Error(firstErr))
	case failures > 0:
		c.logger.Debug("some cgroup pressure files could not be read in this interval",
			zap.Int("failed", failures), zap.Int("files", attempts), zap.Error(firstErr))
	}
}

// rootReady reports whether the root exists and is a cgroup v2 tree, with one warning per episode otherwise.
func (c *cgroupScraper) rootReady() bool {
	if _, err := os.Stat(c.cfg.Path); err != nil {
		if !c.pathWarned {
			c.logger.Warn("cgroup path cannot be read; trying again at the next interval",
				zap.String("path", c.cfg.Path), zap.Error(err))
			c.pathWarned = true
		}
		return false
	}
	c.pathWarned = false
	if _, err := os.Stat(filepath.Join(c.cfg.Path, "cgroup.controllers")); err != nil {
		if !c.controllersWarned {
			c.logger.Warn("cgroup.controllers is missing, so this is not a cgroup v2 tree; no per-cgroup pressure is emitted",
				zap.String("path", c.cfg.Path), zap.Error(err))
			c.controllersWarned = true
		}
		return false
	}
	c.controllersWarned = false
	return true
}

// selectCgroups lists the tree, applies the globs and the level, and keeps the first max_cgroups by path.
func (c *cgroupScraper) selectCgroups(ctx context.Context) []cgroupEntry {
	var listing cgroupListing
	c.walk(c.cfg.Path, "", 1, &listing)
	switch {
	case listing.unreadable > 0 && !c.listWarned:
		c.logger.Warn("some cgroup directories cannot be listed and are left out of the scrape",
			zap.String("path", c.cfg.Path), zap.Int("directories", listing.unreadable), zap.Error(listing.firstErr))
		c.listWarned = true
	case listing.unreadable == 0:
		c.listWarned = false
	}
	found := listing.entries

	kept := found[:0]
	for _, entry := range found {
		if c.wanted(entry) {
			kept = append(kept, entry)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].rel < kept[j].rel })

	if len(kept) <= c.cfg.MaxCgroups {
		c.capWarned = false
		return kept
	}
	left := len(kept) - c.cfg.MaxCgroups
	if c.skipped != nil {
		c.skipped.Add(ctx, int64(left))
	}
	if !c.capWarned {
		c.logger.Warn("more cgroups than max_cgroups; the first ones by path are read and the rest are skipped",
			zap.Int("max_cgroups", c.cfg.MaxCgroups), zap.Int("skipped", left))
		c.capWarned = true
	}
	return kept[:c.cfg.MaxCgroups]
}

// cgroupListing is the result of one walk of the tree.
type cgroupListing struct {
	entries    []cgroupEntry
	unreadable int   // directories that exist but cannot be listed
	firstErr   error // the first of those errors
}

func (c *cgroupScraper) walk(dir, rel string, depth int, out *cgroupListing) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.logger.Debug("cgroup directory is gone", zap.String("dir", dir), zap.Error(err))
			return
		}
		out.unreadable++
		if out.firstErr == nil {
			out.firstErr = err
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		childRel := path.Join(rel, e.Name())
		childDir := filepath.Join(dir, e.Name())
		out.entries = append(out.entries, cgroupEntry{rel: childRel, dir: childDir, info: parseCgroupPath(childRel)})
		if depth < c.cfg.MaxDepth {
			c.walk(childDir, childRel, depth+1, out)
		}
	}
}

func (c *cgroupScraper) wanted(entry cgroupEntry) bool {
	switch c.cfg.Level {
	case levelPod:
		if entry.info.Kind != kindPod {
			return false
		}
	case levelContainer:
		if entry.info.Kind != kindContainer {
			return false
		}
	}
	return matchesAny(c.cfg.Include, entry.rel) && !matchesAny(c.cfg.Exclude, entry.rel)
}

func matchesAny(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if ok, err := doublestar.Match(pattern, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// isGone reports whether err says the cgroup, or its pressure accounting, is gone rather than that reading failed.
func isGone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, errors.ErrUnsupported) || errors.Is(err, syscall.ENODEV)
}

// readPressure parses one pressure file. A missing file (the cgroup is gone), an unsupported one (the cgroup opted out
// of pressure accounting) and an empty one give nil and no error; any other failure is returned.
func (c *cgroupScraper) readPressure(file string) (*pressureFile, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		if isGone(err) {
			c.logger.Debug("cgroup pressure file is not available", zap.String("file", file), zap.Error(err))
			return nil, nil
		}
		return nil, err
	}
	parsed := &pressureFile{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := parsePSILine(line, &parsed.data); err != nil {
			c.logger.Debug("cgroup pressure line cannot be parsed", zap.String("file", file), zap.String("line", line), zap.Error(err))
			continue
		}
		switch {
		case strings.HasPrefix(line, "some "):
			parsed.some = true
		case strings.HasPrefix(line, "full "):
			parsed.full = true
		}
	}
	if !parsed.some && !parsed.full {
		c.logger.Debug("cgroup pressure file is empty", zap.String("file", file))
		return nil, nil
	}
	return parsed, nil
}

// cgroupBuilder groups the series of each pod into one resource.
type cgroupBuilder struct {
	md    pmetric.Metrics
	start pcommon.Timestamp
	now   pcommon.Timestamp

	scopes  map[resourceKey]pmetric.ScopeMetrics
	metrics map[metricKey]pmetric.Metric
}

type resourceKey struct{ uid, qos string }

type metricKey struct {
	resource resourceKey
	name     string
}

func newCgroupBuilder(md pmetric.Metrics, start, now pcommon.Timestamp) *cgroupBuilder {
	return &cgroupBuilder{
		md: md, start: start, now: now,
		scopes:  map[resourceKey]pmetric.ScopeMetrics{},
		metrics: map[metricKey]pmetric.Metric{},
	}
}

func (b *cgroupBuilder) scopeFor(key resourceKey) pmetric.ScopeMetrics {
	if sm, ok := b.scopes[key]; ok {
		return sm
	}
	rm := b.md.ResourceMetrics().AppendEmpty()
	if key.uid != "" {
		rm.Resource().Attributes().PutStr("k8s.pod.uid", key.uid)
		rm.Resource().Attributes().PutStr("k8s.pod.qos", key.qos)
	}
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	sm.Scope().SetVersion(scopeVersion)
	b.scopes[key] = sm
	return sm
}

func (b *cgroupBuilder) metricFor(key resourceKey, name, unit string, isTotal bool) pmetric.Metric {
	mk := metricKey{resource: key, name: name}
	if m, ok := b.metrics[mk]; ok {
		return m
	}
	m := b.scopeFor(key).Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	if isTotal {
		m.SetDescription("Cumulative time tasks in the cgroup were stalled")
		sum := m.SetEmptySum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	} else {
		m.SetDescription("Pressure stall information percentage of the cgroup")
		m.SetEmptyGauge()
	}
	b.metrics[mk] = m
	return m
}

// add emits the chosen values of the some and full lines of one file.
func (b *cgroupBuilder) add(entry cgroupEntry, resource string, file *pressureFile, values []string) {
	key := resourceKey{uid: entry.info.PodUID, qos: entry.info.QoS}
	lines := []struct {
		kind    string
		present bool
		avg10   float64
		avg60   float64
		avg300  float64
		total   uint64
	}{
		{"some", file.some, file.data.SomeAvg10, file.data.SomeAvg60, file.data.SomeAvg300, file.data.SomeTotal},
		{"full", file.full, file.data.FullAvg10, file.data.FullAvg60, file.data.FullAvg300, file.data.FullTotal},
	}
	for _, line := range lines {
		if !line.present {
			continue
		}
		for _, value := range values {
			name := fmt.Sprintf("cgroup.pressure.%s.%s.%s", resource, line.kind, value)
			if value == valueTotal {
				dp := b.metricFor(key, name, "us", true).Sum().DataPoints().AppendEmpty()
				dp.SetStartTimestamp(b.start)
				dp.SetTimestamp(b.now)
				dp.SetIntValue(int64(line.total)) //nolint:gosec // microseconds of stall time stay far below 2^63
				b.setAttributes(dp.Attributes(), entry)
				continue
			}
			average := map[string]float64{valueAvg10: line.avg10, valueAvg60: line.avg60, valueAvg300: line.avg300}[value]
			dp := b.metricFor(key, name, "%", false).Gauge().DataPoints().AppendEmpty()
			dp.SetTimestamp(b.now)
			dp.SetDoubleValue(average)
			b.setAttributes(dp.Attributes(), entry)
		}
	}
}

func (b *cgroupBuilder) setAttributes(attrs pcommon.Map, entry cgroupEntry) {
	attrs.PutStr("cgroup.path", entry.rel)
	if entry.info.ContainerID != "" {
		attrs.PutStr("container.id", entry.info.ContainerID)
	}
}
