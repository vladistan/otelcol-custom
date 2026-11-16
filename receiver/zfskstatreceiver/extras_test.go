// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"fmt"
	"os"
	"path/filepath"
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

const txgsHeader = "26 1 0x01 12 3360 4733209253 8388345122467\n" +
	"txg      birth            state ndirty       nread        nwritten     reads    writes   otime        qtime        wtime        stime\n"

// txgRows is a txgs file: closed rows 100 and 102, an open row 103 and a syncing row 101.
func txgsFile() string {
	return txgsHeader +
		"100      1000             C     4096         0            8192         0        2        5000         100          200          3000000000\n" +
		"102      3000             C     65536        0            131072       0        9        5000         100          200          1500000000\n" +
		"101      2000             S     1            0            1            0        1        5000         100          200          9\n" +
		"103      4000             O     0            0            0            0        0        0            0            0            0\n"
}

func datasetMap(t *testing.T, md pmetric.Metrics, metric string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	dps := dataPoints(findMetric(t, md, metric))
	for i := 0; i < dps.Len(); i++ {
		ds, _ := dps.At(i).Attributes().Get("zfs.dataset")
		pool, _ := dps.At(i).Attributes().Get("zfs.pool")
		out[pool.Str()+"|"+ds.Str()] = dps.At(i).IntValue()
	}
	return out
}

// twoPoolTree is two pools with three datasets in all, on the proc layout.
func twoPoolTree() map[string]string {
	return map[string]string{
		"tank/state":       "ONLINE\n",
		"tank/objset-0x1":  objset("tank/home", "nread 4 1\nnunlinks 4 5\n"),
		"tank/objset-0x2":  objset("tank/tmp", "nread 4 2\nnunlinks 4 6\n"),
		"vault/state":      "ONLINE\n",
		"vault/objset-0x3": objset("vault/data", "nread 4 3\nnunlinks 4 7\n"),
	}
}

func TestDatasetIncludeAndExcludeKeepTheMatchingDatasets(t *testing.T) {
	tests := map[string]struct {
		include, exclude []string
		want             []string
	}{
		"empty include reads all": {nil, nil, []string{"tank|tank/home", "tank|tank/tmp", "vault|vault/data"}},
		"include a pool":          {[]string{"tank/*"}, nil, []string{"tank|tank/home", "tank|tank/tmp"}},
		"include and exclude":     {[]string{"tank/*"}, []string{"tank/tmp"}, []string{"tank|tank/home"}},
		"exclude only":            {nil, []string{"tank/tmp"}, []string{"tank|tank/home", "vault|vault/data"}},
		"exclude wins":            {[]string{"tank/tmp"}, []string{"tank/*"}, nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for src := range map[string]bool{"proc": true, "sysctl": true} {
				cfg := hostConfig()
				cfg.Datasets = true
				cfg.DatasetInclude, cfg.DatasetExclude = tt.include, tt.exclude
				var s *scraper
				if src == "proc" {
					cfg.KstatPath = writeTree(t, twoPoolTree())
					s, _ = newTestScraper(cfg)
				} else {
					f := newFakeSysctl()
					f.addPool("tank", "ONLINE")
					f.addDataset("tank", "0x1", "tank/home", 1, 0, 0, 0)
					f.addDataset("tank", "0x2", "tank/tmp", 2, 0, 0, 0)
					f.addPool("vault", "ONLINE")
					f.addDataset("vault", "0x3", "vault/data", 3, 0, 0, 0)
					cfg.Source = sourceSysctl
					s, _ = sysctlScraper(cfg, f)
				}

				md := scrapeOnce(t, s)

				var got []string
				if len(tt.want) > 0 {
					for k := range datasetMap(t, md, "zfs.dataset.io.bytes") {
						got = append(got, k)
					}
				} else {
					assert.NotContains(t, metricNames(md), "zfs.dataset.io.bytes", src)
				}
				assert.ElementsMatch(t, tt.want, got, src)
			}
		})
	}
}

func TestDatasetFilterRunsBeforeTheCap(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = datasetTree(t, 120)
	cfg.Datasets = true
	cfg.MaxDatasets = 5
	cfg.DatasetInclude = []string{"tank/d11*"}
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Len(t, datasetMap(t, md, "zfs.dataset.io.bytes"), 5)
	assert.Len(t, warnings(logs), 1, "ten datasets match and the cap is five")
}

func TestDatasetUnlinksOnBothSources(t *testing.T) {
	cfg := hostConfig()
	cfg.Datasets = true
	cfg.KstatPath = writeTree(t, twoPoolTree())
	procS, _ := newTestScraper(cfg)
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addDataset("tank", "0x1", "tank/home", 1, 0, 0, 0)
	sysCfg := sysctlConfig()
	sysCfg.Datasets = true
	sysS, _ := sysctlScraper(sysCfg, f)

	procMd, sysMd := scrapeOnce(t, procS), scrapeOnce(t, sysS)

	unlinks := findMetric(t, procMd, "zfs.dataset.unlinks")
	assert.Equal(t, "{unlink}", unlinks.Unit())
	assert.True(t, unlinks.Sum().IsMonotonic())
	assert.Equal(t, int64(5), pointFor(t, unlinks, map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/home"}).IntValue())
	assert.Equal(t, int64(99), pointFor(t, findMetric(t, sysMd, "zfs.dataset.unlinks"), map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/home"}).IntValue())
}

func TestDatasetKstatWithABadFieldOrNoNameIsHandledFieldByField(t *testing.T) {
	cfg := hostConfig()
	cfg.Datasets = true
	cfg.KstatPath = writeTree(t, map[string]string{
		"tank/state":      "ONLINE\n",
		"tank/objset-0x1": objset("tank/home", "writes 4 oops\nnwritten 4 9\n"),
		"tank/objset-0x2": "50 1 0x01 7 2160 5 6\nname type data\nnwritten 4 100\n",
	})
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	assert.Equal(t, 1, dataPoints(bytes).Len(), "only the nwritten of the named dataset")
	assert.NotContains(t, metricNames(md), "zfs.dataset.io.operations", "writes is not a number")
}

func TestTxgMetricsComeFromTheNewestClosedRow(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsFile()})
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	dirty := findMetric(t, md, "zfs.pool.txg.dirty")
	assert.Equal(t, pmetric.MetricTypeGauge, dirty.Type())
	assert.Equal(t, "By", dirty.Unit())
	assert.Equal(t, int64(65536), pointFor(t, dirty, map[string]string{"zfs.pool": "tank"}).IntValue())
	assert.Equal(t, int64(131072), pointFor(t, findMetric(t, md, "zfs.pool.txg.written"), map[string]string{"zfs.pool": "tank"}).IntValue())
	sync := findMetric(t, md, "zfs.pool.txg.sync.time")
	assert.Equal(t, "s", sync.Unit())
	assert.InDelta(t, 1.5, pointFor(t, sync, map[string]string{"zfs.pool": "tank"}).DoubleValue(), 1e-9)
	assert.Zero(t, logs.FilterMessageSnippet("txg").Len())
}

func TestTxgOffEmitsNoTxgMetric(t *testing.T) {
	cfg := hostConfig()
	cfg.Txg = false
	cfg.KstatPath = writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsFile()})
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	for _, n := range metricNames(md) {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg."), n)
	}
	assert.Zero(t, logs.FilterMessageSnippet("txg").Len(), "off means no read and no line")
}

func TestTxgIsOnByDefault(t *testing.T) {
	assert.True(t, createDefaultConfig().(*Config).Txg)
}

func TestTxgWithNoClosedRowOrNoFileGivesNoSeriesAndOneInfoLinePerPool(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, map[string]string{
		"open/state": "ONLINE\n",
		"open/txgs":  txgsHeader + "5 1 O 0 0 0 0 0 0 0 0 0\n",
		"none/state": "ONLINE\n",
	})
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	scrapeOnce(t, s)

	for _, n := range metricNames(first) {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg."), n)
	}
	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("txg").Len(), "one line per pool over two scrapes")
}

func TestTxgInfoLineReturnsAfterTheRowAppearsAndGoes(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsHeader})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)
	path := filepath.Join(root, "tank", "txgs")
	scrapeOnce(t, s)
	require.NoError(t, os.WriteFile(path, []byte(txgsFile()), 0o644))
	assert.Contains(t, metricNames(scrapeOnce(t, s)), "zfs.pool.txg.dirty")
	require.NoError(t, os.WriteFile(path, []byte(txgsHeader), 0o644))

	scrapeOnce(t, s)

	assert.Equal(t, 2, logs.FilterMessageSnippet("txg").Len())
}

func TestParseTxgs(t *testing.T) {
	tests := map[string]struct {
		text string
		want txgRow
		ok   bool
	}{
		"closed row":         {txgsHeader + "7 1 C 10 0 20 0 0 0 0 0 30\n", txgRow{10, 20, 30}, true},
		"highest txg wins":   {txgsHeader + "9 1 C 1 0 2 0 0 0 0 0 3\n8 1 C 4 0 5 0 0 0 0 0 6\n", txgRow{1, 2, 3}, true},
		"open row only":      {txgsHeader + "7 1 O 10 0 20 0 0 0 0 0 30\n", txgRow{}, false},
		"empty":              {"", txgRow{}, false},
		"no column line":     {"26 1 0x01 12\n7 1 C 1 0 2 0 0 0 0 0 3\n", txgRow{}, false},
		"short row":          {txgsHeader + "7 1 C 10\n", txgRow{}, false},
		"non numeric number": {txgsHeader + "7 1 C x 0 2 0 0 0 0 0 3\n", txgRow{}, false},
		"missing column":     {"txg birth state ndirty nwritten\n7 1 C 1 2\n", txgRow{}, false},
		"no state column":    {"txg birth ndirty nwritten stime\n7 1 1 2 3\n", txgRow{}, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := parseTxgs(tt.text)

			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
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

func TestLinuxProcSourceEmitsNoSpaceSeries(t *testing.T) {
	cfg := hostConfig()
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	for _, n := range metricNames(md) {
		assert.False(t, strings.HasPrefix(n, "zfs.dataset.space."), n)
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

func TestDefaultsKeepTheOldOutputWhenDatasetsAndTxgAreOff(t *testing.T) {
	cfg := hostConfig()
	cfg.Txg = false
	s, _ := newTestScraper(cfg)

	names := metricNames(scrapeOnce(t, s))

	for _, n := range names {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg.") || strings.HasPrefix(n, "zfs.dataset."), n)
	}
}

func TestConfigValidateRejectsABadDatasetGlob(t *testing.T) {
	for _, key := range []string{"dataset_include", "dataset_exclude"} {
		bad := createDefaultConfig().(*Config)
		if key == "dataset_include" {
			bad.DatasetInclude = []string{"tank/[a"}
		} else {
			bad.DatasetExclude = []string{"tank/[a"}
		}

		err := bad.Validate()

		require.Error(t, err, key)
		assert.Contains(t, err.Error(), key)
	}
}

func TestConfigValidateAcceptsGoodDatasetGlobs(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.DatasetInclude = []string{"tank/*", "vault/data"}
	cfg.DatasetExclude = []string{"tank/tmp"}

	require.NoError(t, cfg.Validate())
}

func TestNoLibzfsExecCgoOrDevZfsInTheReceiverSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	forbidden := []string{"os/" + "exec", "/dev/" + "zfs", "lib" + "zfs", `import "` + "C" + `"`}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(e.Name())
		require.NoError(t, err)
		for _, word := range forbidden {
			assert.NotContains(t, string(body), word, fmt.Sprintf("%s must not use %s", e.Name(), word))
		}
	}
}
