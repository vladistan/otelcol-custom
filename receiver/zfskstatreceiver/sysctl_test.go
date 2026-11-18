// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var fakeBoot = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)

// fakeSysctl is a kstat.zfs tree: names in walk order and the values behind them.
type fakeSysctl struct {
	names     []string
	uints     map[string]uint64
	strs      map[string]string
	readErr   map[string]error
	vanishAt  map[string]bool // listed by the walk, gone when its name is asked
	absent    bool
	lookupErr error
	nextErr   error
	nameErr   error
	bootErr   error
	mounts    []mount
	mountsErr error
}

func newFakeSysctl() *fakeSysctl {
	return &fakeSysctl{uints: map[string]uint64{}, strs: map[string]string{}, readErr: map[string]error{}, vanishAt: map[string]bool{}}
}

func (f *fakeSysctl) addUint(name string, v uint64) {
	f.names = append(f.names, name)
	f.uints[name] = v
}

func (f *fakeSysctl) addStr(name, v string) {
	f.names = append(f.names, name)
	f.strs[name] = v
}

func (f *fakeSysctl) addPool(pool, state string) {
	f.addStr("kstat.zfs."+pool+".misc.state", state)
}

func (f *fakeSysctl) addDataset(pool, id, name string, nread, nwritten, reads, writes uint64) {
	base := "kstat.zfs." + pool + ".dataset.objset-" + id + "."
	f.addStr(base+"dataset_name", name)
	f.addUint(base+"writes", writes)
	f.addUint(base+"nwritten", nwritten)
	f.addUint(base+"reads", reads)
	f.addUint(base+"nread", nread)
	f.addUint(base+"nunlinks", 99)
}

func (f *fakeSysctl) fns() sysctlFns {
	return sysctlFns{
		lookupOID: func(name string) ([]int32, error) {
			if f.lookupErr != nil {
				return nil, f.lookupErr
			}
			if f.absent || name != "kstat.zfs" {
				return nil, syscall.ENOENT
			}
			return []int32{5}, nil
		},
		nextOID: func(oid []int32) ([]int32, error) {
			if f.nextErr != nil {
				return nil, f.nextErr
			}
			idx := 1
			if len(oid) == 2 {
				idx = int(oid[1]) + 1
			}
			switch {
			case idx <= len(f.names):
				return []int32{5, int32(idx)}, nil
			case idx == len(f.names)+1:
				return []int32{6}, nil // a sibling tree: the walk must stop here
			}
			return nil, syscall.ENOENT
		},
		oidName: func(oid []int32) (string, error) {
			if f.nameErr != nil {
				return "", f.nameErr
			}
			name := f.names[oid[1]-1]
			if f.vanishAt[name] {
				return "", syscall.ENOENT
			}
			return name, nil
		},
		getUint64: func(name string) (uint64, error) {
			if err := f.readErr[name]; err != nil {
				return 0, err
			}
			v, ok := f.uints[name]
			if !ok || f.vanishAt[name] {
				return 0, syscall.ENOENT
			}
			return v, nil
		},
		getString: func(name string) (string, error) {
			if err := f.readErr[name]; err != nil {
				return "", err
			}
			v, ok := f.strs[name]
			if !ok || f.vanishAt[name] {
				return "", syscall.ENOENT
			}
			return v, nil
		},
		bootTime: func() (time.Time, error) { return fakeBoot, f.bootErr },
		getfsstat: func() ([]mount, error) {
			return f.mounts, f.mountsErr
		},
	}
}

// standardFake has the ARC rows, three pools and two datasets on tank.
func standardFake() *fakeSysctl {
	f := newFakeSysctl()
	f.addUint("kstat.zfs.misc.arcstats.hits", 123456)
	f.addUint("kstat.zfs.misc.arcstats.misses", 7890)
	f.addUint("kstat.zfs.misc.arcstats.size", 4294967296)
	f.addUint("kstat.zfs.misc.arcstats.c_max", 1)
	f.addPool("tank", "ONLINE")
	f.addDataset("tank", "0x36", "tank/home", 1000, 2000, 10, 20)
	f.addDataset("tank", "0x40", "tank/data", 3000, 4000, 30, 40)
	f.addPool("backup", "DEGRADED")
	f.addPool("scratch", "ONLINE")
	return f
}

func sysctlScraper(cfg *Config, f *fakeSysctl) (*scraper, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	return newScraperWith(logger, cfg, newSysctlSource(logger, cfg, f.fns())), logs
}

func sysctlConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Source = sourceSysctl
	return cfg
}

func poolNamesOf(t *testing.T, md pmetric.Metrics, metric string) []string {
	t.Helper()
	var out []string
	dps := dataPoints(findMetric(t, md, metric))
	for i := 0; i < dps.Len(); i++ {
		v, _ := dps.At(i).Attributes().Get("zfs.pool")
		out = append(out, v.Str())
	}
	return out
}

func TestResolvedSource(t *testing.T) {
	auto := sourceProc
	if runtime.GOOS == "freebsd" {
		auto = sourceSysctl
	}
	tests := []struct{ source, want string }{
		{"", auto}, {"auto", auto}, {"proc", "proc"}, {"sysctl", "sysctl"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			cfg := &Config{Source: tt.source}

			assert.Equal(t, tt.want, cfg.resolvedSource())
		})
	}
}

func TestSourceUnsetOnLinuxReadsTheProcFixtures(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.Skip("auto picks sysctl on FreeBSD")
	}
	cfg := hostConfig()
	cfg.Source = ""
	s := newScraper(zap.NewNop(), cfg)

	md := scrapeOnce(t, s)

	assert.IsType(t, &procSource{}, s.source)
	assert.Equal(t, int64(1048576), pointFor(t, findMetric(t, md, "zfs.pool.io.bytes"), map[string]string{"zfs.pool": "tank", "direction": "read"}).IntValue())
}

func TestSourceSysctlSelectsTheSysctlReader(t *testing.T) {
	cfg := sysctlConfig()

	s := newScraper(zap.NewNop(), cfg)

	assert.IsType(t, &sysctlSource{}, s.source)
}

func TestSysctlArcMetricsMatchTheProcNamesAndAttributes(t *testing.T) {
	s, logs := sysctlScraper(sysctlConfig(), standardFake())

	md := scrapeOnce(t, s)

	size := findMetric(t, md, "zfs.arc.size")
	assert.Equal(t, "By", size.Unit())
	assert.Equal(t, pmetric.MetricTypeGauge, size.Type())
	assert.Equal(t, int64(4294967296), dataPoints(size).At(0).IntValue())
	hits := findMetric(t, md, "zfs.arc.hits")
	assert.Equal(t, "{hit}", hits.Unit())
	assert.True(t, hits.Sum().IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, hits.Sum().AggregationTemporality())
	assert.Equal(t, int64(123456), dataPoints(hits).At(0).IntValue())
	assert.Equal(t, int64(7890), dataPoints(findMetric(t, md, "zfs.arc.misses")).At(0).IntValue())
	assert.Empty(t, warnings(logs))
}

func TestSysctlArcStatsOffYieldsNoArcMetrics(t *testing.T) {
	cfg := sysctlConfig()
	cfg.ArcStats = false
	s, _ := sysctlScraper(cfg, standardFake())

	names := metricNames(scrapeOnce(t, s))

	assert.Equal(t, []string{"zfs.pool.state"}, names)
}

func TestSysctlPoolStateIsAnInfoGaugeForEachPool(t *testing.T) {
	s, _ := sysctlScraper(sysctlConfig(), standardFake())

	md := scrapeOnce(t, s)

	state := findMetric(t, md, "zfs.pool.state")
	require.Equal(t, pmetric.MetricTypeGauge, state.Type())
	assert.Equal(t, "1", state.Unit())
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "tank", "state": "ONLINE"}).IntValue())
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "backup", "state": "DEGRADED"}).IntValue())
	assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": "scratch", "state": "ONLINE"}).IntValue())
}

func TestSysctlPoolFiltersAndVanishedPools(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config, *fakeSysctl)
		want   []string
	}{
		{"three pools", func(*Config, *fakeSysctl) {}, []string{"tank", "backup", "scratch"}},
		{"pools filter", func(c *Config, _ *fakeSysctl) { c.Pools = []string{"backup", "tank"} }, []string{"tank", "backup"}},
		{"exclude", func(c *Config, _ *fakeSysctl) { c.ExcludePools = []string{"backup"} }, []string{"tank", "scratch"}},
		{"exclude wins over pools", func(c *Config, _ *fakeSysctl) {
			c.Pools = []string{"tank", "backup"}
			c.ExcludePools = []string{"tank"}
		}, []string{"backup"}},
		{"state gone after the walk", func(_ *Config, f *fakeSysctl) {
			f.vanishAt["kstat.zfs.backup.misc.state"] = true
		}, []string{"tank", "scratch"}},
		{"node gone during the walk", func(_ *Config, f *fakeSysctl) {
			f.vanishAt["kstat.zfs.backup.misc.state"] = true
			delete(f.strs, "kstat.zfs.backup.misc.state")
		}, []string{"tank", "scratch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, f := sysctlConfig(), standardFake()
			tt.mutate(cfg, f)
			s, logs := sysctlScraper(cfg, f)

			md := scrapeOnce(t, s)

			assert.Equal(t, tt.want, poolNamesOf(t, md, "zfs.pool.state"))
			assert.Empty(t, warnings(logs))
		})
	}
}

func TestSysctlPoolNameWithDotsAndTheMiscPrefixAreHandled(t *testing.T) {
	f := newFakeSysctl()
	f.addStr("kstat.zfs.misc.state", "not a pool")
	f.addPool("a.b", "ONLINE")
	f.addPool("a", "FAULTED")
	f.addDataset("a", "0x1", "a/x", 1, 2, 3, 4)
	cfg := sysctlConfig()
	cfg.Datasets = true
	s, _ := sysctlScraper(cfg, f)

	md := scrapeOnce(t, s)

	assert.Equal(t, []string{"a.b", "a"}, poolNamesOf(t, md, "zfs.pool.state"))
	assert.Equal(t, 2, dataPoints(findMetric(t, md, "zfs.dataset.io.bytes")).Len(), "only pool a has a dataset")
}

func TestSysctlWithoutAPoolIoKstatYieldsStateAndNoIoMetricsAndNoRepeatingWarning(t *testing.T) {
	s, logs := sysctlScraper(sysctlConfig(), standardFake())

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	for _, md := range []pmetric.Metrics{first, second} {
		names := metricNames(md)
		assert.Contains(t, names, "zfs.pool.state")
		for _, n := range names {
			assert.NotContains(t, n, "zfs.pool.io.")
		}
	}
	assert.Empty(t, warnings(logs))
}

func TestDatasetsUnsetEmitsNoDatasetSeriesOnEitherSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":              ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x36":     objset("tank/home", "writes 4 5\n"),
		"tank/objset-0x40":     objset("tank/data", "writes 4 6\n"),
		"tank/state":           "ONLINE\n",
		"unrelated/objset-0x1": "x",
	})
	proc := hostConfig()
	proc.KstatPath = root
	procScraper, _ := newTestScraper(proc)
	sysScraper, _ := sysctlScraper(sysctlConfig(), standardFake())

	for _, s := range []*scraper{procScraper, sysScraper} {
		for _, name := range metricNames(scrapeOnce(t, s)) {
			assert.NotContains(t, name, "zfs.dataset.")
		}
	}
}

// objset builds a proc objset file: two header lines, the dataset_name row and the given rows.
func objset(name, rows string) string {
	return "50 1 0x01 7 2160 5 6\nname type data\ndataset_name 7 " + name + "\n" + rows
}

func TestDatasetMetricsOnBothSources(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x36": objset("tank/home", "writes 4 20\nnwritten 4 2000\nreads 4 10\nnread 4 1000\nnunlinks 4 7\nnewfield 4 1\n"),
		"tank/objset-0x40": objset("tank/data", "nread 4 3000\nnwritten 4 4000\nreads 4 30\nwrites 4 40\n"),
	})
	procCfg := hostConfig()
	procCfg.KstatPath = root
	procCfg.Datasets = true
	sysCfg := sysctlConfig()
	sysCfg.Datasets = true
	procScraper, _ := newTestScraper(procCfg)
	sysScraper, _ := sysctlScraper(sysCfg, standardFake())

	for name, s := range map[string]*scraper{"proc": procScraper, "sysctl": sysScraper} {
		t.Run(name, func(t *testing.T) {
			md := scrapeOnce(t, s)

			bytes := findMetric(t, md, "zfs.dataset.io.bytes")
			assert.Equal(t, "By", bytes.Unit())
			assert.True(t, bytes.Sum().IsMonotonic())
			assert.Equal(t, pmetric.AggregationTemporalityCumulative, bytes.Sum().AggregationTemporality())
			assert.Equal(t, 4, dataPoints(bytes).Len())
			assert.Equal(t, int64(1000), pointFor(t, bytes, map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/home", "direction": "read"}).IntValue())
			assert.Equal(t, int64(2000), pointFor(t, bytes, map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/home", "direction": "write"}).IntValue())
			assert.Equal(t, int64(3000), pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/data", "direction": "read"}).IntValue())
			ops := findMetric(t, md, "zfs.dataset.io.operations")
			assert.Equal(t, "{operation}", ops.Unit())
			assert.True(t, ops.Sum().IsMonotonic())
			assert.Equal(t, 4, dataPoints(ops).Len())
			assert.Equal(t, int64(10), pointFor(t, ops, map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/home", "direction": "read"}).IntValue())
			assert.Equal(t, int64(40), pointFor(t, ops, map[string]string{"zfs.dataset": "tank/data", "direction": "write"}).IntValue())
		})
	}
}

func TestDatasetObjsetOddContentIsHandledByName(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":         ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x1": objset("tank/short", "nread 4 5\nwrites 4 x\ngarbage\n"),
		"tank/objset-0x2": "50 1 0x01 7 2160 5 6\nname type data\nnread 4 5\n",
		"tank/objset-0x3": "",
		"tank/notobjset":  objset("tank/other", "nread 4 1\n"),
		"tank/dbufs":      "ignored",
	})
	require.NoError(t, os.Mkdir(filepath.Join(root, "tank", "objset-0x4"), 0o755))
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	require.Equal(t, 1, dataPoints(bytes).Len(), "only tank/short has a name and a nread")
	assert.Equal(t, int64(5), pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/short", "direction": "read"}).IntValue())
	assert.NotContains(t, metricNames(md), "zfs.dataset.io.operations", "writes was not a number")
	assert.Empty(t, warnings(logs))
}

func TestDatasetObjsetUnreadableFileWarnsOnce(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/io": ioHeader + "nread nwritten\n1 2\n"})
	require.NoError(t, os.Symlink("objset-0x9", filepath.Join(root, "tank", "objset-0x9")), "a link loop cannot be opened")
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	s, logs := newTestScraper(cfg)

	scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.Len(t, warnings(logs), 1)
}

func TestSysctlDatasetFieldsAreMatchedByName(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addStr("kstat.zfs.tank.dataset.objset-0x1.dataset_name", "tank/a")
	f.addUint("kstat.zfs.tank.dataset.objset-0x1.nread", 11)
	f.addUint("kstat.zfs.tank.dataset.objset-0x1.brandnew", 5)
	f.addUint("kstat.zfs.tank.dataset.objset-0x2.nread", 12) // no name: dropped
	f.addStr("kstat.zfs.tank.dataset.objset-0x3.dataset_name", "tank/gone")
	f.vanishAt["kstat.zfs.tank.dataset.objset-0x3.dataset_name"] = true
	f.addStr("kstat.zfs.tank.dataset.objset-0x4.dataset_name", "")
	f.addUint("kstat.zfs.tank.dataset.objsetnodot", 1)
	f.addUint("kstat.zfs.tank.dataset.objset-0x5.dataset_name.extra", 1)
	f.addStr("kstat.zfs.tank.dataset.objset-0x6.dataset_name", "tank/b")
	f.addUint("kstat.zfs.tank.dataset.objset-0x6.reads", 3)
	f.vanishAt["kstat.zfs.tank.dataset.objset-0x6.reads"] = true
	cfg := sysctlConfig()
	cfg.Datasets = true
	cfg.ArcStats = false
	s, logs := sysctlScraper(cfg, f)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	require.Equal(t, 1, dataPoints(bytes).Len())
	assert.Equal(t, int64(11), pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/a", "direction": "read"}).IntValue())
	assert.NotContains(t, metricNames(md), "zfs.dataset.io.operations")
	assert.Empty(t, warnings(logs))
}

func TestSumsStartAtHostBootOnBothSources(t *testing.T) {
	boot := pcommon.NewTimestampFromTime(fakeBoot)
	cfg := sysctlConfig()
	cfg.Datasets = true
	s, _ := sysctlScraper(cfg, standardFake())
	assert.True(t, fakeBoot.Equal(s.start))

	md := scrapeOnce(t, s)

	for _, name := range []string{"zfs.arc.hits", "zfs.arc.misses", "zfs.dataset.io.bytes", "zfs.dataset.io.operations"} {
		dps := dataPoints(findMetric(t, md, name))
		for i := 0; i < dps.Len(); i++ {
			assert.Equal(t, boot, dps.At(i).StartTimestamp(), name)
		}
	}

	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x36": objset("tank/home", "nread 4 5\nreads 4 5\n"),
	})
	procCfg := hostConfig()
	procCfg.KstatPath = root
	procCfg.Datasets = true
	ps, _ := newTestScraper(procCfg)
	ps.start = fakeBoot
	pmd := scrapeOnce(t, ps)
	for _, name := range []string{"zfs.pool.io.bytes", "zfs.dataset.io.bytes", "zfs.dataset.io.operations"} {
		dps := dataPoints(findMetric(t, pmd, name))
		for i := 0; i < dps.Len(); i++ {
			assert.Equal(t, boot, dps.At(i).StartTimestamp(), name)
		}
	}
}

func TestSysctlBootTimeFallsBackToNow(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		boot time.Time
		err  error
		want time.Time
	}{
		{"read", fakeBoot, nil, fakeBoot},
		{"error", time.Time{}, syscall.EPERM, now},
		{"in the future", now.Add(time.Hour), nil, now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fns := newFakeSysctl().fns()
			fns.bootTime = func() (time.Time, error) { return tt.boot, tt.err }
			src := newSysctlSource(zap.NewNop(), sysctlConfig(), fns)

			assert.True(t, tt.want.Equal(src.bootTime(now)))
		})
	}
}

func TestSysctlWithoutKstatZfsSendsNothingAndLogsOneInfoLine(t *testing.T) {
	f := standardFake()
	f.absent = true
	s, logs := sysctlScraper(sysctlConfig(), f)

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	assert.Equal(t, 0, first.ResourceMetrics().Len())
	assert.Equal(t, 0, second.ResourceMetrics().Len())
	assert.Equal(t, 1, logs.FilterMessageSnippet("kstat.zfs not found").Len())
	assert.Empty(t, warnings(logs))
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).Len(), "the default config logs no other info line")

	f.absent = false
	assert.Contains(t, metricNames(scrapeOnce(t, s)), "zfs.pool.state", "ZFS loaded later is picked up")
	f.absent = true
	scrapeOnce(t, s)
	assert.Equal(t, 2, logs.FilterMessageSnippet("kstat.zfs not found").Len(), "a new absence logs again")
}

func TestReceiverStartsAndSendsNothingWithoutKstatZfs(t *testing.T) {
	f := newFakeSysctl()
	f.absent = true
	sink := new(consumertest.MetricsSink)
	cfg := sysctlConfig()
	r := newTestReceiver(t, cfg, sink)
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	r.scraper = newScraperWith(logger, cfg, newSysctlSource(logger, cfg, f.fns()))

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Empty(t, sink.AllMetrics())
	assert.Equal(t, 1, logs.Len())
}

func TestSysctlErrorWarnsOncePerEpisode(t *testing.T) {
	tests := []struct {
		name    string
		breakIt func(*fakeSysctl, error)
		fix     func(*fakeSysctl)
	}{
		{"lookup", func(f *fakeSysctl, err error) { f.lookupErr = err }, func(f *fakeSysctl) { f.lookupErr = nil }},
		{"walk", func(f *fakeSysctl, err error) { f.nextErr = err }, func(f *fakeSysctl) { f.nextErr = nil }},
		{"oid name", func(f *fakeSysctl, err error) { f.nameErr = err }, func(f *fakeSysctl) { f.nameErr = nil }},
		{"value", func(f *fakeSysctl, err error) { f.readErr["kstat.zfs.misc.arcstats.size"] = err }, func(f *fakeSysctl) { delete(f.readErr, "kstat.zfs.misc.arcstats.size") }},
		{"pool state", func(f *fakeSysctl, err error) { f.readErr["kstat.zfs.tank.misc.state"] = err }, func(f *fakeSysctl) { delete(f.readErr, "kstat.zfs.tank.misc.state") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := standardFake()
			s, logs := sysctlScraper(sysctlConfig(), f)
			tt.breakIt(f, syscall.EIO)

			scrapeOnce(t, s)
			require.Len(t, warnings(logs), 1)
			scrapeOnce(t, s)
			assert.Len(t, warnings(logs), 1, "no second warning in the same episode")

			tt.fix(f)
			assert.Contains(t, metricNames(scrapeOnce(t, s)), "zfs.pool.state")
			assert.Len(t, warnings(logs), 1)

			tt.breakIt(f, syscall.EIO)
			scrapeOnce(t, s)
			assert.Len(t, warnings(logs), 2, "a new episode warns again")
		})
	}
}

func TestSysctlErrorOnOneValueKeepsTheOtherMetrics(t *testing.T) {
	f := standardFake()
	f.readErr["kstat.zfs.misc.arcstats.hits"] = errors.New("boom")
	s, logs := sysctlScraper(sysctlConfig(), f)

	names := metricNames(scrapeOnce(t, s))

	assert.NotContains(t, names, "zfs.arc.hits")
	assert.Contains(t, names, "zfs.arc.misses")
	assert.Contains(t, names, "zfs.pool.state")
	assert.Len(t, warnings(logs), 1)
}

func TestSysctlNotFoundErrorsDoNotWarn(t *testing.T) {
	f := standardFake()
	f.nextErr = syscall.ENOENT
	s, logs := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.Contains(t, metricNames(md), "zfs.arc.size", "arc needs no walk")
	assert.NotContains(t, metricNames(md), "zfs.pool.state")
	assert.Empty(t, warnings(logs))
}

func TestScrapeStopsOnACanceledContext(t *testing.T) {
	s, _ := sysctlScraper(sysctlConfig(), standardFake())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.scrape(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestScrapeReturnsASourceError(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = filepath.Join("testdata", "host", "arcstats", "notadir")
	s, _ := newTestScraper(cfg)

	_, err := s.scrape(context.Background())

	assert.Error(t, err)
}

func TestSelectedAppliesPoolsAndExcludePools(t *testing.T) {
	cfg := &Config{Pools: []string{"a", "b"}, ExcludePools: []string{"b"}}

	assert.True(t, cfg.selected("a"))
	assert.False(t, cfg.selected("b"))
	assert.False(t, cfg.selected("c"))
	assert.True(t, (&Config{}).selected("c"))
}

func TestNonTestSourcesDoNotImportExecOrCgo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		checked++
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err, file)
		for _, imp := range parsed.Imports {
			assert.NotContains(t, []string{`"os/exec"`, `"C"`}, imp.Path.Value, file)
		}
	}
	assert.Greater(t, checked, 5)
}

// The receiver reads the kernel through ioctls and sysctls only, and names the control device
// in the factory defaults and nowhere else. Parsing skips comments and prose in strings.
func TestNoLibzfsExecOrCgoInTheReceiverSourceAndTheNodeNameOnlyInTheDefaults(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	device := "/dev/" + "zfs"
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		require.NoError(t, err, file)
		for _, imp := range parsed.Imports {
			assert.NotEqual(t, `"os/exec"`, imp.Path.Value, file)
			assert.NotEqual(t, `"C"`, imp.Path.Value, file)
			assert.NotContains(t, imp.Path.Value, "libzfs", file)
		}
		namesDevice := false
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			namesDevice = namesDevice || (ok && lit.Kind == token.STRING && strings.Contains(lit.Value, device))
			return true
		})
		assert.Equal(t, file == "factory.go", namesDevice, "%s: control device name in a string", file)
	}
}

// recordingScraper builds a sysctl scraper over fns and records every name asked of the kernel.
func recordingScraper(cfg *Config, fns sysctlFns) (*scraper, *observer.ObservedLogs, *[]string) {
	var reads []string
	inner := fns
	fns.getString = func(name string) (string, error) { reads = append(reads, name); return inner.getString(name) }
	fns.getUint64 = func(name string) (uint64, error) { reads = append(reads, name); return inner.getUint64(name) }
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	return newScraperWith(logger, cfg, newSysctlSource(logger, cfg, fns)), logs, &reads
}

func newest(f *fakeSysctl, body string, pools ...string) {
	for _, p := range pools {
		f.addPool(p, "ONLINE")
		f.addStr("kstat.zfs."+p+".txgs", body)
	}
}

func TestSysctlTxgReadsTheTxgsNodeOfThePool(t *testing.T) {
	f := newFakeSysctl()
	newest(f, txgsFile(), "tank")
	s, _ := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.Equal(t, int64(65536), pointFor(t, findMetric(t, md, "zfs.pool.txg.dirty"), poolAttr("tank")).IntValue())
}

func TestSysctlTxgPrefersTheTxgsNodeOverTheMiscOne(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addStr("kstat.zfs.tank.misc.txgs", strings.Replace(txgsFile(), "65536", "111", 1))
	f.addStr("kstat.zfs.tank.txgs", txgsFile())
	s, _ := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.Equal(t, int64(65536), pointFor(t, findMetric(t, md, "zfs.pool.txg.dirty"), poolAttr("tank")).IntValue())
}

func TestSysctlTxgWithNeitherNodeGivesNoSeriesAndOneInfoLinePerPool(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addPool("bare", "ONLINE")
	s, logs := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.NotContains(t, metricNames(md), "zfs.pool.txg.dirty")
	assert.Equal(t, 2, logs.FilterMessageSnippet("no txgs sysctl").Len())
}

// capturedFake builds the sysctl tree of the captured listing: names in listing order, a state
// for each pool, a txgs body for each pool that has the node and a name for each objset.
func capturedFake(t *testing.T) *fakeSysctl {
	t.Helper()
	body, err := os.ReadFile("testdata/freebsd/truenas-core-13.1-kstat-zfs-listing.txt")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.Greater(t, len(lines), 600)
	f := newFakeSysctl()
	for _, name := range lines[2:] {
		switch {
		case strings.HasSuffix(name, ".misc.state"):
			f.addStr(name, "ONLINE")
		case strings.HasSuffix(name, ".txgs"):
			f.addStr(name, txgsFile())
		case strings.HasSuffix(name, ".dataset_name"):
			f.addStr(name, strings.TrimSuffix(strings.TrimPrefix(name, "kstat.zfs."), ".dataset_name"))
		default:
			f.addUint(name, 1)
		}
	}
	return f
}

func TestTheCapturedTrueNASListingGivesStateAndTxgForBothPools(t *testing.T) {
	s, _ := sysctlScraper(sysctlConfig(), capturedFake(t))

	md := scrapeOnce(t, s)

	state := findMetric(t, md, "zfs.pool.state")
	for _, pool := range []string{"P1", "boot-pool"} {
		assert.Equal(t, int64(1), pointFor(t, state, map[string]string{"zfs.pool": pool, "state": "ONLINE"}).IntValue(), pool)
		assert.Equal(t, int64(65536), pointFor(t, findMetric(t, md, "zfs.pool.txg.dirty"), poolAttr(pool)).IntValue(), pool)
	}
}

func TestAStateWhoseValueCannotBeReadGivesOneWarningThatNamesTheOIDAndTheType(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addPool("odd", "")
	f.addStr("kstat.zfs.tank.txgs", txgsFile())
	fns := f.fns()
	fns.oidType = func(name string) (string, error) { return "opaque", nil }
	s, logs, _ := recordingScraper(sysctlConfig(), fns)

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	assert.Equal(t, []string{"tank"}, poolNamesOf(t, first, "zfs.pool.state"))
	assert.Equal(t, []string{"tank"}, poolNamesOf(t, second, "zfs.pool.state"))
	require.Len(t, warnings(logs), 1)
	ctx := warnings(logs)[0].ContextMap()
	assert.Equal(t, "kstat.zfs.odd.misc.state", ctx["oid"])
	assert.Equal(t, "opaque", ctx["type"])
	assert.Contains(t, metricNames(first), "zfs.pool.txg.dirty", "the other series of the pool and of tank stay")
}

func TestAStateOfControlBytesIsNotAState(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("odd", "\x01\x02\x03")
	s, logs := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.NotContains(t, metricNames(md), "zfs.pool.state")
	require.Len(t, warnings(logs), 1)
	assert.Equal(t, "unknown", warnings(logs)[0].ContextMap()["type"], "no type function reports unknown")
}

func TestAStateWarningReturnsAfterTheStateReadsCleanlyAgain(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("odd", "")
	s, logs := sysctlScraper(sysctlConfig(), f)

	scrapeOnce(t, s)
	f.strs["kstat.zfs.odd.misc.state"] = "ONLINE"
	good := scrapeOnce(t, s)
	f.strs["kstat.zfs.odd.misc.state"] = ""
	scrapeOnce(t, s)

	assert.Contains(t, metricNames(good), "zfs.pool.state")
	assert.Len(t, warnings(logs), 2)
}

func TestAStateWithTrailingSpaceOrNulIsTrimmed(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "DEGRADED\x00\n")
	s, _ := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.Equal(t, int64(1), pointFor(t, findMetric(t, md, "zfs.pool.state"), map[string]string{"state": "DEGRADED"}).IntValue())
}

func TestWithDatasetsOffNoObjsetNodeIsRead(t *testing.T) {
	cfg := sysctlConfig()
	cfg.Datasets = false
	f := standardFake()
	s, _, reads := recordingScraper(cfg, f.fns())

	scrapeOnce(t, s)

	require.NotEmpty(t, *reads)
	for _, name := range *reads {
		assert.NotContains(t, name, ".dataset.objset-", name)
	}
}

func TestWithDatasetsOnTheObjsetNodesAreRead(t *testing.T) {
	cfg := sysctlConfig()
	cfg.Datasets = true
	s, _, reads := recordingScraper(cfg, standardFake().fns())

	scrapeOnce(t, s)

	joined := strings.Join(*reads, " ")
	assert.Contains(t, joined, ".dataset.objset-")
}

func TestReadRawStringGrowsTheBufferWhenTheKernelSaysItIsTooSmall(t *testing.T) {
	var sizes []int
	text := strings.Repeat("x", 10000)

	got, err := readRawString(func(old []byte) (int, error) {
		sizes = append(sizes, len(old))
		if len(old) < len(text) {
			return 0, syscall.ENOMEM
		}
		return copy(old, text+"\x00\x00"), nil
	})

	require.NoError(t, err)
	assert.Equal(t, text, got, "trailing NUL bytes are dropped")
	assert.Equal(t, []int{4096, 8192, 16384}, sizes)
}

func TestReadRawStringStopsAtTheCap(t *testing.T) {
	calls := 0

	_, err := readRawString(func([]byte) (int, error) { calls++; return 0, syscall.ENOMEM })

	assert.ErrorIs(t, err, syscall.ENOMEM)
	assert.Equal(t, 9, calls, "4 KiB doubled to 1 MiB")
}

func TestReadRawStringPassesOtherErrorsThroughAndReadsEmptyAsEmpty(t *testing.T) {
	_, err := readRawString(func([]byte) (int, error) { return 0, syscall.EIO })
	assert.ErrorIs(t, err, syscall.EIO)

	got, err := readRawString(func([]byte) (int, error) { return 0, nil })
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestKindNameNamesTheSysctlTypes(t *testing.T) {
	for kind, want := range map[uint32]string{
		2: "int", 3: "string", 4: "int64", 5: "opaque", 6: "uint", 7: "long", 8: "ulong", 9: "uint64", 0xa: "uint8",
		0xb: "uint16", 0xc: "int8", 0xd: "int16", 0xe: "int32", 0xf: "uint32", 1: "node", 0: "unknown",
		0x80000003: "string", // the flag bits above the type are ignored
	} {
		assert.Equal(t, want, kindName(kind), "kind %#x", kind)
	}
}

func TestValidStateAcceptsNamesAndRejectsGarbage(t *testing.T) {
	for state, want := range map[string]bool{
		"ONLINE": true, "DEGRADED": true, "UNAVAIL": true, "": false, "\x01": false, "ON LINE": false,
		strings.Repeat("A", 65): false,
	} {
		assert.Equal(t, want, validState(state), "%q", state)
	}
}

func TestSysctlStateOfTheStandardFakeIsUnchanged(t *testing.T) {
	s, _ := sysctlScraper(sysctlConfig(), standardFake())

	md := scrapeOnce(t, s)

	assert.ElementsMatch(t, []string{"tank", "backup", "scratch"}, poolNamesOf(t, md, "zfs.pool.state"))
}

func TestSysctlTxgReadsTheMiscTxgsNode(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addStr("kstat.zfs.tank.misc.txgs", txgsFile())
	f.addPool("bare", "ONLINE")
	s, logs := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.Equal(t, int64(65536), pointFor(t, findMetric(t, md, "zfs.pool.txg.dirty"), map[string]string{"zfs.pool": "tank"}).IntValue())
	assert.Equal(t, []string{"tank"}, poolNamesOf(t, md, "zfs.pool.txg.dirty"))
	assert.Equal(t, 1, logs.FilterMessageSnippet("no txgs sysctl").Len(), "bare has none")
}

func TestSysctlTxgNodeWithNoClosedRowOrAReadErrorGivesNoSeries(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addStr("kstat.zfs.tank.misc.txgs", txgsHeader)
	f.addPool("bad", "ONLINE")
	f.addStr("kstat.zfs.bad.misc.txgs", "x")
	f.readErr["kstat.zfs.bad.misc.txgs"] = syscall.EIO
	s, logs := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	assert.NotContains(t, metricNames(md), "zfs.pool.txg.dirty")
	assert.Equal(t, 1, logs.FilterMessageSnippet("no closed transaction group").Len())
	assert.Len(t, warnings(logs), 1, "the read error is one warning")
}

func rootMount(from, fsType string, blocks, free, avail uint64) mount {
	return mount{from: from, fsType: fsType, blockSize: 4096, blocks: blocks, free: free, avail: avail}
}

func TestSysctlSpaceComesFromTheMountOfTheRootDataset(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.mounts = []mount{
		rootMount("tank/home", "zfs", 1000, 900, 900),
		rootMount("/dev/ada0p2", "ufs", 5, 1, 1),
		rootMount("tank", "zfs", 1000, 600, 590),
	}
	s, _ := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	attrs := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank"}
	used := findMetric(t, md, "zfs.dataset.space.used")
	assert.Equal(t, "By", used.Unit())
	assert.Equal(t, pmetric.MetricTypeGauge, used.Type())
	assert.Equal(t, int64(400*4096), pointFor(t, used, attrs).IntValue())
	assert.Equal(t, int64(590*4096), pointFor(t, findMetric(t, md, "zfs.dataset.space.available"), attrs).IntValue())
}

func TestSysctlSpaceWithoutAMountedRootGivesNoSeriesAndOneDebugLine(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.mounts = []mount{rootMount("tank/home", "zfs", 1, 1, 1)}
	s, logs := sysctlScraper(sysctlConfig(), f)

	first := scrapeOnce(t, s)
	scrapeOnce(t, s)

	assert.NotContains(t, metricNames(first), "zfs.dataset.space.used")
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.DebugLevel).FilterMessageSnippet("not mounted").Len())
}

func TestSysctlSpaceWithoutAMountListOrWithItsErrorsIsQuietOrOneWarning(t *testing.T) {
	tests := map[string]struct {
		err  error
		nilF bool
		warn int
	}{
		"no hook":     {nil, true, 0},
		"not found":   {syscall.ENOENT, false, 0},
		"other error": {syscall.EIO, false, 1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeSysctl()
			f.addPool("tank", "ONLINE")
			f.mountsErr = tt.err
			fns := f.fns()
			if tt.nilF {
				fns.getfsstat = nil
			}
			cfg := sysctlConfig()
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)
			s := newScraperWith(logger, cfg, newSysctlSource(logger, cfg, fns))

			md := scrapeOnce(t, s)

			assert.NotContains(t, metricNames(md), "zfs.dataset.space.used")
			assert.Len(t, warnings(logs), tt.warn)
			assert.Contains(t, metricNames(md), "zfs.pool.state")
		})
	}
}

func TestSpaceUsedNeverUnderflowsWhenFreeExceedsBlocks(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.mounts = []mount{rootMount("tank", "zfs", 10, 20, 5)}
	s, _ := sysctlScraper(sysctlConfig(), f)

	md := scrapeOnce(t, s)

	attrs := map[string]string{"zfs.pool": "tank"}
	assert.Equal(t, int64(0), pointFor(t, findMetric(t, md, "zfs.dataset.space.used"), attrs).IntValue())
}
