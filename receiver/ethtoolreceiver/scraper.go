// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"errors"
	"slices"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	scopeName  = "github.com/vladistan/otelcol-custom/receiver/ethtoolreceiver"
	metricName = "network.allowance.exceeded"
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
}

func newScraper(logger *zap.Logger, cfg *Config, nic nicReader, start time.Time) *scraper {
	return &scraper{
		logger:     logger,
		nic:        nic,
		drivers:    toSet(cfg.Drivers),
		interfaces: toSet(cfg.Interfaces),
		start:      pcommon.NewTimestampFromTime(start),
		warned:     map[string]bool{},
	}
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

	var sum pmetric.Sum
	matched := false
	ts := pcommon.NewTimestampFromTime(now)
	for _, name := range names {
		if len(s.interfaces) > 0 {
			if _, ok := s.interfaces[name]; !ok {
				continue
			}
		}
		counters, ok := s.read(name)
		matched = matched || ok
		for _, allowance := range allowances {
			v, found := counters[allowance.stat]
			if !found {
				continue
			}
			if md.MetricCount() == 0 {
				sum = newSum(md)
			}
			dp := sum.DataPoints().AppendEmpty()
			dp.Attributes().PutStr("network.interface.name", name)
			dp.Attributes().PutStr("allowance", allowance.label)
			dp.SetStartTimestamp(s.start)
			dp.SetTimestamp(ts)
			dp.SetIntValue(int64(v)) //nolint:gosec // a packet counter stays far below 2^63
		}
	}
	s.noteMatch(matched)
	return md
}

const (
	warnPermission = "permission"
	warnMissing    = "missing"
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

func newSum(md pmetric.Metrics) pmetric.Sum {
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	m := sm.Metrics().AppendEmpty()
	m.SetName(metricName)
	m.SetDescription("Packets queued or dropped because the instance exceeded a network allowance")
	m.SetUnit("{packet}")
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	return sum
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
