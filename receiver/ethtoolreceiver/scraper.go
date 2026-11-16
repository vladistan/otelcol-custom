// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"cmp"
	"errors"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	scopeName       = "github.com/vladistan/otelcol-custom/receiver/ethtoolreceiver"
	metricName      = "network.allowance.exceeded"
	availableMetric = "network.allowance.available"
	statisticMetric = "network.interface.statistic"
	// availableStat is the ENA statistic that gives the connections the instance can still track.
	availableStat = "conntrack_allowance_available"
)

// allowances maps the five ENA statistic names to the allowance attribute.
var allowances = []struct{ stat, label string }{
	{"bw_in_allowance_exceeded", "bandwidth_in"},
	{"bw_out_allowance_exceeded", "bandwidth_out"},
	{"pps_allowance_exceeded", "pps"},
	{"conntrack_allowance_exceeded", "conntrack"},
	{"linklocal_allowance_exceeded", "linklocal"},
}

type scraper struct {
	logger     *zap.Logger
	nic        nicReader
	drivers    map[string]struct{}
	interfaces map[string]struct{}
	start      pcommon.Timestamp

	warned        map[string]bool // one warning per kind and interface
	noMatchLogged bool

	rules         []rule
	hit           []bool // per rule: matched a statistic in this scrape
	maxStatistics int
}

// rule is a compiled statistics entry.
type rule struct {
	re          *regexp.Regexp
	match, kind string
}

func newScraper(logger *zap.Logger, cfg *Config, nic nicReader, start time.Time) *scraper {
	sc := &scraper{
		logger:     logger,
		nic:        nic,
		drivers:    toSet(cfg.Drivers),
		interfaces: toSet(cfg.Interfaces),
		start:      pcommon.NewTimestampFromTime(start),
		warned:     map[string]bool{},

		rules:         compileRules(cfg.Statistics),
		maxStatistics: cmp.Or(cfg.MaxStatistics, defaultMaxStatistics),
	}
	sc.hit = make([]bool, len(sc.rules))
	return sc
}

// compileRules skips an entry whose pattern does not compile; Validate rejects those at load.
func compileRules(entries []Statistic) []rule {
	var rules []rule
	for _, e := range entries {
		if re, err := regexp.Compile(e.Match); err == nil {
			rules = append(rules, rule{re, e.Match, e.Type})
		}
	}
	return rules
}

func toSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// scrape reads every selected interface; one that fails is skipped for this read.
func (s *scraper) scrape(now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	names, err := s.nic.interfaces()
	if err != nil {
		s.logger.Warn("Failed to list network interfaces", zap.Error(err))
		return md
	}

	out := &output{md: md, start: s.start, ts: pcommon.NewTimestampFromTime(now)}
	var chosen []picked
	matched := false
	for _, name := range names {
		if len(s.interfaces) > 0 {
			if _, ok := s.interfaces[name]; !ok {
				continue
			}
		}
		counters, ok := s.read(name)
		matched = matched || ok
		for _, allowance := range allowances {
			if v, found := counters[allowance.stat]; found {
				out.exceeded(name, allowance.label, v)
			}
		}
		if counters != nil {
			s.available(out, name, counters)
			chosen = append(chosen, s.pick(name, counters)...)
		}
	}
	s.emitStatistics(out, chosen, matched)
	s.noteMatch(matched)
	return md
}

// output builds the metrics of one scrape; each metric is created with its first point.
type output struct {
	md        pmetric.Metrics
	sm        pmetric.ScopeMetrics
	haveSM    bool
	start, ts pcommon.Timestamp

	sum, avail, statSum, statGauge pmetric.NumberDataPointSlice
	haveSum, haveAvail             bool
	haveStatSum, haveStatGauge     bool
}

func (o *output) scope() pmetric.ScopeMetrics {
	if !o.haveSM {
		o.sm = o.md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
		o.sm.Scope().SetName(scopeName)
		o.haveSM = true
	}
	return o.sm
}

func (o *output) point(dps pmetric.NumberDataPointSlice, iface string, cumulative bool) pmetric.NumberDataPoint {
	dp := dps.AppendEmpty()
	dp.Attributes().PutStr("network.interface.name", iface)
	if cumulative {
		dp.SetStartTimestamp(o.start)
	}
	dp.SetTimestamp(o.ts)
	return dp
}

func (o *output) exceeded(iface, label string, v uint64) {
	if !o.haveSum {
		o.sum, o.haveSum = newSum(o.scope()), true
	}
	dp := o.point(o.sum, iface, true)
	dp.Attributes().PutStr("allowance", label)
	dp.SetIntValue(int64(v)) //nolint:gosec // a packet counter stays far below 2^63
}

func (o *output) available(iface string, v uint64) {
	if !o.haveAvail {
		m := o.scope().Metrics().AppendEmpty()
		m.SetName(availableMetric)
		m.SetDescription("Tracked connections the instance can still create before it reaches its connection tracking allowance")
		m.SetUnit("{connection}")
		o.avail, o.haveAvail = m.SetEmptyGauge().DataPoints(), true
	}
	dp := o.point(o.avail, iface, false)
	dp.Attributes().PutStr("allowance", "conntrack")
	dp.SetIntValue(int64(v)) //nolint:gosec // a connection count stays far below 2^63
}

func (o *output) statistic(iface, stat, kind string, v uint64) {
	var dps *pmetric.NumberDataPointSlice
	if kind == typeSum {
		if !o.haveStatSum {
			o.statSum, o.haveStatSum = newStatistic(o.scope(), true), true
		}
		dps = &o.statSum
	} else {
		if !o.haveStatGauge {
			o.statGauge, o.haveStatGauge = newStatistic(o.scope(), false), true
		}
		dps = &o.statGauge
	}
	dp := o.point(*dps, iface, kind == typeSum)
	dp.Attributes().PutStr("statistic", stat)
	dp.SetIntValue(int64(min(v, math.MaxInt64)))
}

func newStatistic(sm pmetric.ScopeMetrics, cumulative bool) pmetric.NumberDataPointSlice {
	m := sm.Metrics().AppendEmpty()
	m.SetName(statisticMetric)
	m.SetDescription("A driver statistic selected by the statistics list")
	m.SetUnit("1")
	if !cumulative {
		return m.SetEmptyGauge().DataPoints()
	}
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	return sum.DataPoints()
}

// available emits the conntrack headroom, or notes once per interface that the driver lacks it.
func (s *scraper) available(out *output, iface string, counters map[string]uint64) {
	if v, ok := counters[availableStat]; ok {
		out.available(iface, v)
		delete(s.warned, warnKey(infoNoAvailable, iface))
		return
	}
	if s.firstWarning(infoNoAvailable, iface) {
		s.logger.Info("Network interface does not report conntrack_allowance_available; no network.allowance.available point",
			zap.String("interface", iface))
	}
}

// picked is one statistic chosen by the statistics list.
type picked struct {
	iface, name, kind string
	value             uint64
}

// pick returns the statistics of one interface that an entry selects; a name that several
// entries match goes to the first.
func (s *scraper) pick(iface string, counters map[string]uint64) []picked {
	if len(s.rules) == 0 {
		return nil
	}
	var out []picked
	for name, v := range counters {
		for i, r := range s.rules {
			if r.re.MatchString(name) {
				s.hit[i] = true
				out = append(out, picked{iface, name, r.kind, v})
				break
			}
		}
	}
	return out
}

// emitStatistics caps the picked series in sorted order and logs entries that matched
// nothing; the entry line waits until an interface has a matching driver.
func (s *scraper) emitStatistics(out *output, all []picked, matched bool) {
	if len(s.rules) == 0 {
		return
	}
	for i, r := range s.rules {
		key := "entry/" + strconv.Itoa(i)
		if s.hit[i] {
			delete(s.warned, key)
		} else if matched && s.firstWarning("entry", strconv.Itoa(i)) {
			s.logger.Info("A statistics entry matches no statistic", zap.String("match", r.match))
		}
		s.hit[i] = false
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].iface != all[j].iface {
			return all[i].iface < all[j].iface
		}
		return all[i].name < all[j].name
	})
	limit := s.maxStatistics
	if len(all) > limit {
		if s.firstWarning(warnCap, "") {
			s.logger.Warn("Statistics above max_statistics are not reported",
				zap.Int("matched", len(all)), zap.Int("max_statistics", limit))
		}
		all = all[:limit]
	} else {
		delete(s.warned, warnKey(warnCap, ""))
	}
	for _, p := range all {
		out.statistic(p.iface, p.name, p.kind, p.value)
	}
}

const (
	warnPermission  = "permission"
	warnMissing     = "missing"
	infoNoAvailable = "no-available"
	warnCap         = "cap"
)

func warnKey(kind, iface string) string { return kind + "/" + iface }

// firstWarning reports whether this is the first warning of the kind for the interface.
func (s *scraper) firstWarning(kind, iface string) bool {
	key := warnKey(kind, iface)
	if s.warned[key] {
		return false
	}
	s.warned[key] = true
	return true
}

func newSum(sm pmetric.ScopeMetrics) pmetric.NumberDataPointSlice {
	m := sm.Metrics().AppendEmpty()
	m.SetName(metricName)
	m.SetDescription("Packets queued or dropped because the instance exceeded a network allowance")
	m.SetUnit("{packet}")
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	return sum.DataPoints()
}

// noteMatch logs one info line while no interface has a matching driver.
func (s *scraper) noteMatch(matched bool) {
	if matched {
		s.noMatchLogged = false
		return
	}
	if !s.noMatchLogged {
		s.noMatchLogged = true
		s.logger.Info("No network interface with a matching driver; nothing to report",
			zap.Strings("drivers", sortedKeys(s.drivers)),
			zap.Strings("interfaces", sortedKeys(s.interfaces)))
	}
}

// read returns the allowance counters of one interface and whether its driver
// matched. A failed or dropped read returns no counters.
func (s *scraper) read(name string) (map[string]uint64, bool) {
	driverName, err := s.nic.driver(name)
	if err != nil {
		s.report(name, err)
		return nil, false
	}
	if _, ok := s.drivers[driverName]; !ok {
		return nil, false
	}
	delete(s.warned, warnKey(warnPermission, name))

	count, err := s.nic.statCount(name)
	if err != nil {
		s.report(name, err)
		return nil, true
	}
	names, err := s.nic.statNames(name, count)
	if err != nil {
		s.report(name, err)
		return nil, true
	}
	values, err := s.nic.statValues(name, count)
	if err != nil {
		s.report(name, err)
		return nil, true
	}
	if len(names) != len(values) {
		s.report(name, errCountChanged)
		return nil, true
	}
	// A driver that renumbers its statistics keeps the count; names read again after
	// the values must match the first read, or the pairing is wrong.
	again, err := s.nic.statNames(name, count)
	if err != nil {
		s.report(name, err)
		return nil, true
	}
	if !slices.Equal(names, again) {
		s.report(name, errCountChanged)
		return nil, true
	}

	counters := make(map[string]uint64, len(allowances))
	for i, n := range names {
		counters[n] = values[i]
	}
	s.warnMissing(name, counters)
	return counters, true
}

func (s *scraper) warnMissing(name string, counters map[string]uint64) {
	var missing []string
	for _, allowance := range allowances {
		if _, ok := counters[allowance.stat]; !ok {
			missing = append(missing, allowance.stat)
		}
	}
	if len(missing) == 0 || !s.firstWarning(warnMissing, name) {
		return
	}
	s.logger.Warn("Network interface lacks allowance counters; an older driver version may not report them",
		zap.String("interface", name), zap.Strings("missing", missing))
}

// report logs an ioctl failure at the level the receiver gives it.
func (s *scraper) report(name string, err error) {
	switch {
	case errors.Is(err, errNotSupported), errors.Is(err, errNoDevice):
		s.logger.Debug("Skipping interface", zap.String("interface", name), zap.Error(err))
	case errors.Is(err, errCountChanged):
		s.logger.Debug("Statistic count changed; read dropped until the next interval", zap.String("interface", name))
	case errors.Is(err, errPermission):
		if !s.firstWarning(warnPermission, name) {
			return
		}
		s.logger.Warn("The ethtool ioctl was denied; check the seccomp or security profile of the container (reads need no capability)",
			zap.String("interface", name), zap.Error(err))
	default:
		s.logger.Warn("Failed to read interface statistics", zap.String("interface", name), zap.Error(err))
	}
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
