// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const ioHeader = "18 3 0x00 1 80 2056369846284 3085012421498\n"

func hostConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.KstatPath = filepath.Join("testdata", "host")
	return cfg
}

func newTestScraper(cfg *Config) (*scraper, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return newScraper(zap.New(core), cfg), logs
}

func scrapeOnce(t *testing.T, s *scraper) pmetric.Metrics {
	t.Helper()
	md, err := s.scrape(context.Background())
	require.NoError(t, err)
	return md
}

// writeTree writes files under a new temp dir and returns it; keys are slash paths.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	return root
}

func metricNames(md pmetric.Metrics) []string {
	var names []string
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			for k := 0; k < sms.At(j).Metrics().Len(); k++ {
				names = append(names, sms.At(j).Metrics().At(k).Name())
			}
		}
	}
	return names
}

func findMetric(t *testing.T, md pmetric.Metrics, name string) pmetric.Metric {
	t.Helper()
	require.Equal(t, 1, md.ResourceMetrics().Len())
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i)
		}
	}
	require.Failf(t, "metric not found", "%s in %v", name, metricNames(md))
	return pmetric.Metric{}
}

func dataPoints(m pmetric.Metric) pmetric.NumberDataPointSlice {
	if m.Type() == pmetric.MetricTypeGauge {
		return m.Gauge().DataPoints()
	}
	return m.Sum().DataPoints()
}

// pointFor returns the point whose attributes include every given pair.
func pointFor(t *testing.T, m pmetric.Metric, want map[string]string) pmetric.NumberDataPoint {
	t.Helper()
	dps := dataPoints(m)
	for i := 0; i < dps.Len(); i++ {
		match := true
		for k, v := range want {
			got, ok := dps.At(i).Attributes().Get(k)
			if !ok || got.Str() != v {
				match = false
			}
		}
		if match {
			return dps.At(i)
		}
	}
	require.Failf(t, "point not found", "%s %v", m.Name(), want)
	return pmetric.NumberDataPoint{}
}

func warnings(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterLevelExact(zapcore.WarnLevel).All()
}

func TestScrapeTwoPoolsYieldsMetricsForBoth(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, 4, dataPoints(bytes).Len(), "two pools, two directions")
	pointFor(t, bytes, map[string]string{"zfs.pool": "tank"})
	pointFor(t, bytes, map[string]string{"zfs.pool": "backup"})
}

func TestScrapePoolsFilterKeepsTheListedPoolOnly(t *testing.T) {
	cfg := hostConfig()
	cfg.Pools = []string{"tank"}
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, 2, dataPoints(bytes).Len())
	for i := 0; i < dataPoints(bytes).Len(); i++ {
		pool, _ := dataPoints(bytes).At(i).Attributes().Get("zfs.pool")
		assert.Equal(t, "tank", pool.Str())
	}
}

func TestScrapeExcludePoolsRemovesAPool(t *testing.T) {
	cfg := hostConfig()
	cfg.ExcludePools = []string{"tank"}
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, 2, dataPoints(bytes).Len())
	for i := 0; i < dataPoints(bytes).Len(); i++ {
		pool, _ := dataPoints(bytes).At(i).Attributes().Get("zfs.pool")
		assert.Equal(t, "backup", pool.Str())
	}
}

func TestScrapeBytesAndOperationsMatchTheFixture(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, "By", bytes.Unit())
	assert.True(t, bytes.Sum().IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, bytes.Sum().AggregationTemporality())
	assert.Equal(t, int64(1048576), pointFor(t, bytes, map[string]string{"zfs.pool": "tank", "direction": "read"}).IntValue())
	assert.Equal(t, int64(2097152), pointFor(t, bytes, map[string]string{"zfs.pool": "tank", "direction": "write"}).IntValue())
	ops := findMetric(t, md, "zfs.pool.io.operations")
	assert.Equal(t, "{operation}", ops.Unit())
	assert.Equal(t, int64(100), pointFor(t, ops, map[string]string{"zfs.pool": "tank", "direction": "read"}).IntValue())
	assert.Equal(t, int64(200), pointFor(t, ops, map[string]string{"zfs.pool": "tank", "direction": "write"}).IntValue())
	assert.Equal(t, int64(8), pointFor(t, ops, map[string]string{"zfs.pool": "backup", "direction": "write"}).IntValue())
}

func TestScrapeQueueBusyTimesAreInSeconds(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	wait := findMetric(t, md, "zfs.pool.io.wait.busy.time")
	run := findMetric(t, md, "zfs.pool.io.run.busy.time")
	assert.Equal(t, "s", wait.Unit())
	assert.True(t, wait.Sum().IsMonotonic())
	assert.InDelta(t, 1.5, pointFor(t, wait, map[string]string{"zfs.pool": "tank"}).DoubleValue(), 1e-9)
	assert.InDelta(t, 2.5, pointFor(t, run, map[string]string{"zfs.pool": "tank"}).DoubleValue(), 1e-9)
	assert.InDelta(t, 0.1, pointFor(t, wait, map[string]string{"zfs.pool": "backup"}).DoubleValue(), 1e-9)
}

func TestScrapeBusyTimeDescriptionsSayItIsNotLatency(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	for _, name := range []string{"zfs.pool.io.wait.busy.time", "zfs.pool.io.run.busy.time"} {
		desc := findMetric(t, md, name).Description()
		assert.Contains(t, desc, "not empty", name)
		assert.Contains(t, desc, "not a per-operation latency", name)
	}
}

func TestScrapeOldBusyTimeNamesAreGone(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	names := metricNames(scrapeOnce(t, s))

	assert.NotContains(t, names, "zfs.pool.io.wait.time")
	assert.NotContains(t, names, "zfs.pool.io.service.time")
}

func TestScrapeQueueLengthsAreGaugesWithAQueueAttribute(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	queue := findMetric(t, md, "zfs.pool.io.queue.length")
	require.Equal(t, pmetric.MetricTypeGauge, queue.Type())
	assert.Equal(t, int64(2), pointFor(t, queue, map[string]string{"zfs.pool": "tank", "queue": "wait"}).IntValue())
	assert.Equal(t, int64(3), pointFor(t, queue, map[string]string{"zfs.pool": "tank", "queue": "run"}).IntValue())
}

func TestScrapeStateIsAnInfoGaugeOfOne(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	state := findMetric(t, md, "zfs.pool.state")
	require.Equal(t, pmetric.MetricTypeGauge, state.Type())
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "tank", "state": "ONLINE"}).IntValue())
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "backup", "state": "DEGRADED"}).IntValue())
}

func TestScrapeUnreadableStateSkipsOnlyTheStateMetric(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io": ioHeader + "nread nwritten\n10 20\n",
	})
	require.NoError(t, os.Mkdir(filepath.Join(root, "tank", "state"), 0o755), "a directory cannot be read as a file")
	cfg := hostConfig()
	cfg.KstatPath = root
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Equal(t, []string{"zfs.pool.io.bytes"}, metricNames(md))
}

func TestScrapeArcStatsOnYieldsTheThreeArcMetrics(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	size := findMetric(t, md, "zfs.arc.size")
	hits := findMetric(t, md, "zfs.arc.hits")
	misses := findMetric(t, md, "zfs.arc.misses")
	assert.Equal(t, "By", size.Unit())
	assert.Equal(t, pmetric.MetricTypeGauge, size.Type())
	assert.Equal(t, int64(4294967296), dataPoints(size).At(0).IntValue())
	assert.Equal(t, "{hit}", hits.Unit())
	assert.True(t, hits.Sum().IsMonotonic())
	assert.Equal(t, int64(123456), dataPoints(hits).At(0).IntValue())
	assert.Equal(t, "{miss}", misses.Unit())
	assert.Equal(t, int64(7890), dataPoints(misses).At(0).IntValue())
}

func TestScrapeArcStatsOffYieldsNoArcMetrics(t *testing.T) {
	cfg := hostConfig()
	cfg.ArcStats = false
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	for _, name := range metricNames(md) {
		assert.NotContains(t, name, "zfs.arc.")
	}
	assert.Contains(t, metricNames(md), "zfs.pool.io.bytes")
}

func TestScrapeIoFieldsAreMatchedByName(t *testing.T) {
	// extra unknown field first, wtime missing, columns reordered
	root := writeTree(t, map[string]string{
		"tank/io": ioHeader + "newfield rcnt nwritten reads nread writes wcnt\n99 7 2048 5 1024 6 4\n",
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, int64(1024), pointFor(t, bytes, map[string]string{"direction": "read"}).IntValue())
	assert.Equal(t, int64(2048), pointFor(t, bytes, map[string]string{"direction": "write"}).IntValue())
	ops := findMetric(t, md, "zfs.pool.io.operations")
	assert.Equal(t, int64(5), pointFor(t, ops, map[string]string{"direction": "read"}).IntValue())
	queue := findMetric(t, md, "zfs.pool.io.queue.length")
	assert.Equal(t, int64(4), pointFor(t, queue, map[string]string{"queue": "wait"}).IntValue())
	assert.Equal(t, int64(7), pointFor(t, queue, map[string]string{"queue": "run"}).IntValue())
	assert.NotContains(t, metricNames(md), "zfs.pool.io.wait.busy.time", "wtime is missing")
	assert.Empty(t, warnings(logs))
}

func TestScrapeMissingKstatPathEmitsNothingAndLogsOneInfoLine(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = filepath.Join(t.TempDir(), "no-zfs")
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	assert.Equal(t, 0, first.ResourceMetrics().Len())
	assert.Equal(t, 0, second.ResourceMetrics().Len())
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).Len(), "one info line over two scrapes")
	assert.Empty(t, warnings(logs))
}

func TestScrapeFindsAKstatPathThatAppearsLater(t *testing.T) {
	parent := t.TempDir()
	cfg := hostConfig()
	cfg.KstatPath = filepath.Join(parent, "zfs")
	s, _ := newTestScraper(cfg)
	assert.Equal(t, 0, scrapeOnce(t, s).ResourceMetrics().Len())

	require.NoError(t, os.MkdirAll(filepath.Join(cfg.KstatPath, "tank"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.KstatPath, "tank", "io"), []byte(ioHeader+"nread nwritten\n1 2\n"), 0o644))

	md := scrapeOnce(t, s)

	assert.Contains(t, metricNames(md), "zfs.pool.io.bytes")
}

func TestScrapeEmptyIoFileSkipsThePoolWithOneWarningPerPool(t *testing.T) {
	root := writeTree(t, map[string]string{
		"bad/io":  "",
		"good/io": ioHeader + "nread nwritten\n10 20\n",
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	for _, md := range []pmetric.Metrics{first, second} {
		bytes := findMetric(t, md, "zfs.pool.io.bytes")
		assert.Equal(t, 2, dataPoints(bytes).Len(), "only the good pool")
		pool, _ := dataPoints(bytes).At(0).Attributes().Get("zfs.pool")
		assert.Equal(t, "good", pool.Str())
	}
	require.Len(t, warnings(logs), 1, "one warning for the bad pool over two scrapes")
	assert.Equal(t, "bad", warnings(logs)[0].ContextMap()["pool"])
}

func TestScrapeShortIoFileIsSkippedAndWarnsAgainAfterItRecovers(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "nread nwritten reads\n10 20\n"})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)
	ioPath := filepath.Join(root, "tank", "io")

	assert.Equal(t, 0, scrapeOnce(t, s).ResourceMetrics().Len(), "fewer values than names")
	require.NoError(t, os.WriteFile(ioPath, []byte(ioHeader+"nread nwritten\n10 20\n"), 0o644))
	assert.Contains(t, metricNames(scrapeOnce(t, s)), "zfs.pool.io.bytes")
	require.NoError(t, os.WriteFile(ioPath, []byte(ioHeader+"nread\n"), 0o644))
	assert.Equal(t, 0, scrapeOnce(t, s).ResourceMetrics().Len())

	assert.Len(t, warnings(logs), 2, "one warning before the recovery, one after it breaks again")
}

func TestScrapeNonNumericIoValueIsSkippedWithAWarning(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "nread nwritten\n10 x\n"})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Equal(t, 0, md.ResourceMetrics().Len())
	assert.Len(t, warnings(logs), 1)
}

func TestScrapePoolWithoutAnIoFileKeepsItsStateAndGivesNoIoPoints(t *testing.T) {
	root := writeTree(t, map[string]string{
		"new/state": "ONLINE\n",
		"tank/io":   ioHeader + "nread nwritten\n1 2\n",
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.pool.io.bytes")
	assert.Equal(t, 2, dataPoints(bytes).Len(), "only tank has io fields")
	pointFor(t, findMetric(t, md, "zfs.pool.state"), map[string]string{"zfs.pool": "new", "state": "ONLINE"})
	assert.Empty(t, warnings(logs))
}

// openzfs21Tree is the layout of a pool on OpenZFS 2.1: no io file.
func openzfs21Tree() map[string]string {
	return map[string]string{
		"Z12/state":         "ONLINE\n",
		"Z12/iostats":       "12 1 0x01 18 4896 1 1\nname type data\ntrim_extents_written 4 0\n",
		"Z12/dmu_tx_assign": "9 1 0x01 32 1 1 1\nname type data\n1 ns 4 5\n",
		"Z12/txgs":          "txg birth state ndirty nread nwritten reads writes otime qtime wtime stime\n",
		"Z12/objset-0x36":   "30 1 0x01 7 2160 1 1\nname type data\ndataset_name 7 Z12/data\nnread 4 10\nnwritten 4 20\nreads 4 1\nwrites 4 2\n",
		"arcstats":          "13 1 0x01 5 1 1 1\nname type data\nsize 4 100\nhits 4 7\nmisses 4 3\n",
	}
}

func TestScrapeOpenZFS21PoolEmitsStateAndNoIoMetricsWithoutWarnings(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, openzfs21Tree())
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	names := metricNames(md)
	assert.Contains(t, names, "zfs.pool.state")
	assert.Contains(t, names, "zfs.arc.size")
	for _, n := range names {
		assert.NotContains(t, n, "zfs.pool.io.", "2.1 has no pool io counters")
	}
	assert.Empty(t, warnings(logs))
}

func TestScrapeOpenZFS21PoolReadsItsDatasetsWhenAsked(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, openzfs21Tree())
	cfg.Datasets = true
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	got := pointFor(t, bytes, map[string]string{"zfs.pool": "Z12", "zfs.dataset": "Z12/data", "direction": "write"})
	assert.Equal(t, int64(20), got.IntValue())
}

func TestScrapeIgnoresFilesBesideThePoolDirectories(t *testing.T) {
	root := writeTree(t, map[string]string{
		"dbufs":   "not a pool\n",
		"tank/io": ioHeader + "nread nwritten\n1 2\n",
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.ArcStats = false
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Equal(t, 2, dataPoints(findMetric(t, md, "zfs.pool.io.bytes")).Len())
	assert.Empty(t, warnings(logs))
}

func TestScrapeCumulativeSumsStartAtTheCounterStart(t *testing.T) {
	s, _ := newTestScraper(hostConfig())
	s.start = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	start := pcommon.NewTimestampFromTime(s.start)

	md := scrapeOnce(t, s)

	dp := pointFor(t, findMetric(t, md, "zfs.pool.io.bytes"), map[string]string{"zfs.pool": "tank", "direction": "read"})
	assert.Equal(t, start, dp.StartTimestamp())
	assert.Greater(t, dp.Timestamp(), start)
	arc := dataPoints(findMetric(t, md, "zfs.arc.hits")).At(0)
	assert.Equal(t, start, arc.StartTimestamp())
}

func TestNewScraperStartIsNeverAfterTheReceiverStart(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	assert.False(t, s.start.After(s.started), "boot time, or the receiver start when boot time is unknown")
}

func TestBootTimeFromUptime(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fallback := now.Add(-time.Minute)
	tests := []struct {
		name string
		body string
		want time.Time
	}{
		{"uptime in seconds", "3600.50 7000.00\n", now.Add(-3600500 * time.Millisecond)},
		{"integer uptime", "90 10\n", now.Add(-90 * time.Second)},
		{"empty file", "", fallback},
		{"not a number", "soon 1\n", fallback},
		{"negative uptime", "-5 1\n", fallback},
		{"uptime in the far future", "1e12 1\n", fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "uptime")
			require.NoError(t, os.WriteFile(path, []byte(tt.body), 0o644))

			assert.True(t, tt.want.Equal(bootTime(path, now, fallback)), "got %v", bootTime(path, now, fallback))
		})
	}

	t.Run("missing file", func(t *testing.T) {
		assert.True(t, fallback.Equal(bootTime(filepath.Join(t.TempDir(), "none"), now, fallback)))
	})
}

func TestScrapeArcStatsMissingFileOrNamesSkipsOnlyArc(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":  ioHeader + "nread nwritten\n1 2\n",
		"arcstats": "9 1 0x01 98 26656 1 2\nname type data\nhits 4 5\ngarbage\nweird 4 x\n",
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Contains(t, metricNames(md), "zfs.arc.hits")
	assert.NotContains(t, metricNames(md), "zfs.arc.misses")
	assert.NotContains(t, metricNames(md), "zfs.arc.size")
	assert.Empty(t, warnings(logs))
}

func TestScrapeArcStatsDirectoryWarnsOnce(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "nread nwritten\n1 2\n"})
	require.NoError(t, os.Mkdir(filepath.Join(root, "arcstats"), 0o755))
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)

	scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Len(t, warnings(logs), 1)
}

func TestScrapeOnlyUnknownFieldsYieldsNothing(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "other another\n1 2\n"})
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.ArcStats = false
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Equal(t, 0, md.ResourceMetrics().Len())
}

func TestScrapeHugeCounterIsClampedNotWrapped(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "nread nwritten\n18446744073709551615 1\n"})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	dp := pointFor(t, findMetric(t, md, "zfs.pool.io.bytes"), map[string]string{"direction": "read"})
	assert.Equal(t, int64(9223372036854775807), dp.IntValue())
}

func TestScrapeScopeNameIsTheModulePath(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	assert.Equal(t, "github.com/vladistan/otelcol-custom/receiver/zfskstatreceiver",
		md.ResourceMetrics().At(0).ScopeMetrics().At(0).Scope().Name())
}
