// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// datasetMap maps "pool|dataset" to the value of each point of a metric; it is empty when the metric is absent.
func datasetMap(t *testing.T, md pmetric.Metrics, metric string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	if !slices.Contains(metricNames(md), metric) {
		return out
	}
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

// twoPoolScrapers builds a proc and a sysctl scraper over the same two pools and three datasets.
func twoPoolScrapers(t *testing.T, include, exclude []string) map[string]*scraper {
	t.Helper()
	procCfg := hostConfig()
	procCfg.Datasets = true
	procCfg.DatasetInclude, procCfg.DatasetExclude = include, exclude
	procCfg.KstatPath = writeTree(t, twoPoolTree())
	procScraper, _ := newTestScraper(procCfg)

	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addDataset("tank", "0x1", "tank/home", 1, 0, 0, 0)
	f.addDataset("tank", "0x2", "tank/tmp", 2, 0, 0, 0)
	f.addPool("vault", "ONLINE")
	f.addDataset("vault", "0x3", "vault/data", 3, 0, 0, 0)
	sysCfg := hostConfig()
	sysCfg.Datasets = true
	sysCfg.DatasetInclude, sysCfg.DatasetExclude = include, exclude
	sysCfg.Source = sourceSysctl
	sysScraper, _ := sysctlScraper(sysCfg, f)

	return map[string]*scraper{"proc": procScraper, "sysctl": sysScraper}
}

func mapKeys(m map[string]int64) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
			for src, s := range twoPoolScrapers(t, tt.include, tt.exclude) {
				md := scrapeOnce(t, s)

				assert.Equal(t, len(tt.want) > 0, slices.Contains(metricNames(md), "zfs.dataset.io.bytes"), src)
				assert.ElementsMatch(t, tt.want, mapKeys(datasetMap(t, md, "zfs.dataset.io.bytes")), src)
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

func TestDefaultMaxDatasetsIs100(t *testing.T) {
	assert.Equal(t, 100, createDefaultConfig().(*Config).MaxDatasets)
}

func TestProcSourceKeepsADatasetNameWithASpace(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x36": objset("tank/my data/a b", "nread 4 77\n"),
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	assert.Equal(t, int64(77), pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/my data/a b", "direction": "read"}).IntValue())
	assert.Empty(t, warnings(logs))
}

func TestProcAndSysctlSourcesAgreeOnADatasetNameWithASpace(t *testing.T) {
	name := "tank/my data"
	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x36": objset(name, "nread 4 5\n"),
	})
	procCfg := hostConfig()
	procCfg.KstatPath = root
	procCfg.Datasets = true
	procS, _ := newTestScraper(procCfg)
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	f.addDataset("tank", "0x36", name, 5, 0, 0, 0)
	sysCfg := sysctlConfig()
	sysCfg.Datasets = true
	sysS, _ := sysctlScraper(sysCfg, f)

	for src, s := range map[string]*scraper{"proc": procS, "sysctl": sysS} {
		md := scrapeOnce(t, s)
		bytes := findMetric(t, md, "zfs.dataset.io.bytes")
		assert.Equal(t, []string{name}, datasetsIn(bytes), src)
		pointFor(t, bytes, map[string]string{"zfs.dataset": name, "direction": "read"})
	}
}

// datasetsIn lists the distinct zfs.dataset values of a metric, sorted.
func datasetsIn(m pmetric.Metric) []string {
	seen := map[string]bool{}
	dps := dataPoints(m)
	for i := 0; i < dps.Len(); i++ {
		if v, ok := dps.At(i).Attributes().Get("zfs.dataset"); ok {
			seen[v.Str()] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func datasetTree(t *testing.T, count int) string {
	t.Helper()
	files := map[string]string{"tank/io": ioHeader + "nread nwritten\n1 2\n"}
	for i := 0; i < count; i++ {
		files[fmt.Sprintf("tank/objset-0x%02x", i)] = objset(fmt.Sprintf("tank/d%03d", i), "nread 4 1\n")
	}
	return writeTree(t, files)
}

func TestMaxDatasetsKeepsTheFirstInSortedOrderAndWarnsOncePerEpisode(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = datasetTree(t, 120)
	cfg.Datasets = true
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)
	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	require.Equal(t, 100, dataPoints(bytes).Len())
	pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/d000", "direction": "read"})
	pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/d099", "direction": "read"})
	for i := 0; i < dataPoints(bytes).Len(); i++ {
		assert.NotEqual(t, "tank/d100", dataPoints(bytes).At(i).Attributes().AsRaw()["zfs.dataset"])
	}
	require.Len(t, warnings(logs), 1)

	scrapeOnce(t, s)
	assert.Len(t, warnings(logs), 1, "the second scrape in the same episode is quiet")
}

func TestMaxDatasetsWarnsAgainAfterTheCountFitsAgain(t *testing.T) {
	root := datasetTree(t, 5)
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	cfg.MaxDatasets = 3
	s, logs := newTestScraper(cfg)

	scrapeOnce(t, s)
	require.Len(t, warnings(logs), 1)
	require.NoError(t, os.Remove(filepath.Join(root, "tank", "objset-0x03")))
	require.NoError(t, os.Remove(filepath.Join(root, "tank", "objset-0x04")))
	scrapeOnce(t, s)
	require.Len(t, warnings(logs), 1, "four datasets or fewer fit, no warning")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "objset-0x09"), []byte(objset("tank/d009", "nread 4 1\n")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "objset-0x0a"), []byte(objset("tank/d010", "nread 4 1\n")), 0o644))
	scrapeOnce(t, s)
	assert.Len(t, warnings(logs), 2, "a new episode warns again")
}

func TestMaxDatasetsZeroMeansNoCap(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = datasetTree(t, 120)
	cfg.Datasets = true
	cfg.MaxDatasets = 0
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	assert.Equal(t, 120, dataPoints(findMetric(t, md, "zfs.dataset.io.bytes")).Len())
	assert.Empty(t, warnings(logs))
}

func TestMaxDatasetsAppliesOnTheSysctlSource(t *testing.T) {
	f := newFakeSysctl()
	f.addPool("tank", "ONLINE")
	for i := 0; i < 5; i++ {
		f.addDataset("tank", fmt.Sprintf("0x%02x", i), fmt.Sprintf("tank/d%03d", i), 1, 1, 1, 1)
	}
	cfg := sysctlConfig()
	cfg.Datasets = true
	cfg.MaxDatasets = 2
	s, logs := sysctlScraper(cfg, f)

	md := scrapeOnce(t, s)

	assert.Equal(t, []string{"tank/d000", "tank/d001"}, datasetsIn(findMetric(t, md, "zfs.dataset.io.bytes")))
	assert.Len(t, warnings(logs), 1)
}

func TestCounterResetMovesTheStartTimeOfThatSeriesOnly(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n100 200\n",
		"tank/objset-0x01": objset("tank/a", "nread 4 500\nnwritten 4 600\n"),
		"tank/objset-0x02": objset("tank/b", "nread 4 500\nnwritten 4 600\n"),
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	s, _ := newTestScraper(cfg)
	boot := pcommon.NewTimestampFromTime(s.start)

	first := scrapeOnce(t, s)
	a1 := pointFor(t, findMetric(t, first, "zfs.dataset.io.bytes"), map[string]string{"zfs.dataset": "tank/a", "direction": "read"})
	assert.Equal(t, boot, a1.StartTimestamp())

	time.Sleep(5 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "objset-0x01"), []byte(objset("tank/a", "nread 4 10\nnwritten 4 700\n")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "objset-0x02"), []byte(objset("tank/b", "nread 4 900\nnwritten 4 700\n")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "io"), []byte(ioHeader+"nread nwritten\n50 300\n"), 0o644))
	second := scrapeOnce(t, s)

	bytes := findMetric(t, second, "zfs.dataset.io.bytes")
	aRead := pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/a", "direction": "read"})
	assert.Equal(t, int64(10), aRead.IntValue())
	assert.Equal(t, aRead.Timestamp(), aRead.StartTimestamp(), "a counter that went down restarts at the scrape time")
	aWrite := pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/a", "direction": "write"})
	assert.Equal(t, boot, aWrite.StartTimestamp(), "a counter that only grew keeps the boot start")
	bRead := pointFor(t, bytes, map[string]string{"zfs.dataset": "tank/b", "direction": "read"})
	assert.Equal(t, boot, bRead.StartTimestamp())

	poolRead := pointFor(t, findMetric(t, second, "zfs.pool.io.bytes"), map[string]string{"zfs.pool": "tank", "direction": "read"})
	assert.Equal(t, poolRead.Timestamp(), poolRead.StartTimestamp(), "pool counters follow the same rule")
}

func TestCounterResetForgetsASeriesThatDisappears(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tank/io":          ioHeader + "nread nwritten\n1 2\n",
		"tank/objset-0x01": objset("tank/a", "nread 4 500\n"),
	})
	cfg := hostConfig()
	cfg.KstatPath = root
	cfg.Datasets = true
	s, _ := newTestScraper(cfg)
	boot := pcommon.NewTimestampFromTime(s.start)

	scrapeOnce(t, s)
	require.NoError(t, os.Remove(filepath.Join(root, "tank", "objset-0x01")))
	scrapeOnce(t, s)
	require.NoError(t, os.WriteFile(filepath.Join(root, "tank", "objset-0x01"), []byte(objset("tank/a", "nread 4 5\n")), 0o644))
	md := scrapeOnce(t, s)

	p := pointFor(t, findMetric(t, md, "zfs.dataset.io.bytes"), map[string]string{"zfs.dataset": "tank/a", "direction": "read"})
	assert.Equal(t, boot, p.StartTimestamp(), "a dataset that came back after an absence is a new series, not a reset")
}
