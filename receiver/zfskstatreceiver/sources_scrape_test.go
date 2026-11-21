// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The FreeBSD path reads the pool state and the txg table from the sysctl tree and the pool and
// dataset space from the control device. These tests run both sources, each over a fake, in one
// scrape and check that every value comes from its own source and from no other.

// statfsUsed and statfsAvail are what the fake mount of the root dataset says; the fake ioctl layer
// says 100 and 900, so a value of either kind shows which source it came from.
const (
	statfsUsed  = 600 * 4096
	statfsAvail = 300 * 4096
)

// recordedSysctl returns the fake tree's calls with every name asked of the kernel appended to reads.
func recordedSysctl(f *fakeSysctl, reads *[]string) sysctlFns {
	fns := f.fns()
	getString, getUint64, mounts := fns.getString, fns.getUint64, fns.getfsstat
	fns.getString = func(name string) (string, error) { *reads = append(*reads, name); return getString(name) }
	fns.getUint64 = func(name string) (uint64, error) { *reads = append(*reads, name); return getUint64(name) }
	fns.getfsstat = func() ([]mount, error) { *reads = append(*reads, "getfsstat"); return mounts() }
	return fns
}

// freebsdTree is a sysctl tree with one pool that reports DEGRADED, a txgs table and a stale
// misc.txgs table, and the statfs mount of the pool's root dataset.
func freebsdTree() *fakeSysctl {
	f := newFakeSysctl()
	f.addPool("tank", "DEGRADED")
	f.addStr("kstat.zfs.tank.misc.txgs", strings.Replace(txgsFile(), "65536", "111", 1))
	f.addStr("kstat.zfs.tank.txgs", txgsFile())
	f.mounts = []mount{rootMount("tank", "zfs", 1000, 400, 300)}
	return f
}

func freebsdConfig() *Config {
	cfg := sysctlConfig()
	cfg.DatasetInclude = []string{"tank", "tank/*"}
	return cfg
}

// freebsdScraper joins the sysctl source over f and the given space source in one scraper.
func freebsdScraper(cfg *Config, f *fakeSysctl, space *spaceSource) (*scraper, *observer.ObservedLogs, *[]string) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	var reads []string
	s := newScraperWith(logger, cfg, newSysctlSource(logger, cfg, recordedSysctl(f, &reads)))
	s.space = space
	return s, logs, &reads
}

func valuesOf(t *testing.T, md pmetric.Metrics, name string) []int64 {
	t.Helper()
	var out []int64
	for _, dp := range allPoints(findMetric(t, md, name)) {
		out = append(out, dp.IntValue())
	}
	return out
}

// pointsNamed returns the points of every metric with this name: a scrape that mixes the ioctl and
// the statfs space writes two metrics of one name, one from each source.
func pointsNamed(md pmetric.Metrics, name string) []pmetric.NumberDataPoint {
	var out []pmetric.NumberDataPoint
	sms := md.ResourceMetrics().At(0).ScopeMetrics()
	for i := 0; i < sms.Len(); i++ {
		for j := 0; j < sms.At(i).Metrics().Len(); j++ {
			if m := sms.At(i).Metrics().At(j); m.Name() == name {
				out = append(out, allPoints(m)...)
			}
		}
	}
	return out
}

func pointsFor(points []pmetric.NumberDataPoint, key, value string) []pmetric.NumberDataPoint {
	var out []pmetric.NumberDataPoint
	for _, dp := range points {
		if v, ok := dp.Attributes().Get(key); ok && v.Str() == value {
			out = append(out, dp)
		}
	}
	return out
}

func TestFreeBSDScrapeTakesEachValueFromItsOwnSourceOnly(t *testing.T) {
	cfg := freebsdConfig()
	f := freebsdTree()
	api := tankAPI(t)
	space, _ := newTestSpace(cfg, api)
	s, _, reads := freebsdScraper(cfg, f, space)

	md := scrapeOnce(t, s)

	// The state is the sysctl text; the ioctl layer says the pool is ONLINE and must not change it.
	state := findMetric(t, md, "zfs.pool.state")
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "tank", "state": "DEGRADED"}).IntValue())
	assert.Len(t, allPoints(state), 1)
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")), "health is the ioctl value")

	// The txg values come from the text table; the stale misc table is never read.
	assert.Equal(t, int64(65536), gaugeInt(t, md, "zfs.pool.txg.dirty", poolAttr("tank")))
	assert.NotContains(t, *reads, "kstat.zfs.tank.misc.txgs")
	assert.Contains(t, *reads, "kstat.zfs.tank.txgs")

	// Pool and dataset space are the ioctl values; the statfs sizes never reach a series.
	assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", poolAttr("tank")))
	root := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank"}
	assert.Equal(t, int64(100), gaugeInt(t, md, "zfs.dataset.space.used", root))
	assert.Equal(t, int64(900), gaugeInt(t, md, "zfs.dataset.space.available", root))
	assert.Len(t, pointsFor(pointsNamed(md, "zfs.dataset.space.used"), "zfs.dataset", "tank"), 1)
	assert.NotContains(t, valuesOf(t, md, "zfs.dataset.space.used"), int64(statfsUsed))
	assert.NotContains(t, valuesOf(t, md, "zfs.dataset.space.available"), int64(statfsAvail))
	assert.Equal(t, int64(100), gaugeInt(t, md, "zfs.dataset.space.used",
		map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/data"}))

	// The ioctl layer is asked for space only: no request names a state or a txg.
	for _, call := range api.calls {
		assert.True(t, strings.HasPrefix(call, "props:") || strings.HasPrefix(call, "stats:") ||
			strings.HasPrefix(call, "kids:") || strings.HasPrefix(call, "decode:") || strings.HasPrefix(call, "objset:"), call)
	}
}

func TestFreeBSDScrapeKeepsTheSysctlSeriesAndTheStatfsSpaceWhenTheDeviceCannotBeOpened(t *testing.T) {
	cfg := freebsdConfig()
	core, devLogs := observer.New(zapcore.DebugLevel)
	space := newSpaceSource(zap.New(core), cfg, func(string) (zfsDevice, error) { return nil, syscall.EACCES })
	s, _, _ := freebsdScraper(cfg, freebsdTree(), space)

	md := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Equal(t, int64(1), pointFor(t, findMetric(t, md, "zfs.pool.state"),
		map[string]string{"zfs.pool": "tank", "state": "DEGRADED"}).IntValue())
	assert.Equal(t, int64(65536), gaugeInt(t, md, "zfs.pool.txg.dirty", poolAttr("tank")))
	root := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank"}
	assert.Equal(t, int64(statfsUsed), gaugeInt(t, md, "zfs.dataset.space.used", root))
	assert.Equal(t, int64(statfsAvail), gaugeInt(t, md, "zfs.dataset.space.available", root))
	assert.NotContains(t, metricNames(md), "zfs.pool.size")
	assert.Equal(t, 1, devLogs.FilterMessageSnippet("Cannot open the ZFS control device").Len())
}

func TestFreeBSDScrapeWithoutZFSIoctlsOnTheSystemKeepsTheStatfsSpaceQuietly(t *testing.T) {
	cfg := freebsdConfig()
	core, devLogs := observer.New(zapcore.DebugLevel)
	space := newSpaceSource(zap.New(core), cfg, func(string) (zfsDevice, error) { return nil, errIoctlUnsupported })
	s, logs, _ := freebsdScraper(cfg, freebsdTree(), space)

	md := scrapeOnce(t, s)

	root := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank"}
	assert.Equal(t, int64(statfsUsed), gaugeInt(t, md, "zfs.dataset.space.used", root))
	assert.Equal(t, int64(65536), gaugeInt(t, md, "zfs.pool.txg.dirty", poolAttr("tank")))
	assert.Empty(t, warnings(logs))
	assert.Empty(t, warnings(devLogs))
}

func TestFreeBSDStatfsSpaceStaysForAPoolTheIoctlLayerDoesNotKnow(t *testing.T) {
	cfg := freebsdConfig()
	f := freebsdTree()
	f.addPool("scratch", "ONLINE")
	f.mounts = append(f.mounts, rootMount("scratch", "zfs", 500, 100, 50))
	api := tankAPI(t)
	api.errs["props:scratch"] = syscall.ENOENT // the pool went away between the list and the read
	space, _ := newTestSpace(cfg, api)
	s, _, _ := freebsdScraper(cfg, f, space)

	md := scrapeOnce(t, s)

	used := pointsNamed(md, "zfs.dataset.space.used")
	tank := pointsFor(used, "zfs.dataset", "tank")
	scratch := pointsFor(used, "zfs.dataset", "scratch")
	require.Len(t, tank, 1)
	require.Len(t, scratch, 1)
	assert.Equal(t, int64(100), tank[0].IntValue(), "tank is the ioctl value")
	assert.Equal(t, int64(400*4096), scratch[0].IntValue(), "scratch is the statfs value")
	assert.Equal(t, int64(1), pointFor(t, findMetric(t, md, "zfs.pool.state"),
		map[string]string{"zfs.pool": "scratch", "state": "ONLINE"}).IntValue())
}
