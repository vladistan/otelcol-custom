// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAChildOutsideItsParentIsSkippedWithOneWarning(t *testing.T) {
	api := tankAPI(t)
	api.kids["tank"] = []string{"other/x", "tank/data"}
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank"})
	s.read([]string{"tank"})

	var names []string
	for _, d := range r.datasets {
		names = append(names, d.name)
	}
	assert.Equal(t, []string{"tank", "tank/data"}, names)
	assert.Len(t, warnings(logs), 1)
}

func TestOnlySelectedDatasetsAreDecoded(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/keep"}
	api := tankAPI(t)
	api.objs["tank/keep"] = propsNV(t, goodDatasetProps())
	api.objs["tank/skip"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/skip", "tank/keep"}
	s, _ := newTestSpace(cfg, api)

	s.read([]string{"tank"})

	assert.Contains(t, api.calls, "decode:tank/keep")
	assert.NotContains(t, api.calls, "decode:tank/skip")
}

func TestAFailedDecodeOfASelectedChildIsAWarningForThatChildOnly(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/*"}
	api := tankAPI(t)
	api.objs["tank/b"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/data", "tank/b"}
	api.errs["decode:tank/data"] = errNVShort
	s, logs := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	require.Len(t, r.datasets, 1)
	assert.Equal(t, "tank/b", r.datasets[0].name)
	require.Len(t, warnings(logs), 1)
	assert.Equal(t, "tank/data", warnings(logs)[0].ContextMap()["zfs.dataset"])
}

func TestCouldMatchBelowFollowsThePatternSegments(t *testing.T) {
	for _, tt := range []struct {
		include []string
		name    string
		want    bool
	}{
		{[]string{"tank"}, "tank", false},
		{[]string{"tank/*"}, "tank", true},
		{[]string{"tank/*"}, "tank/a", false},
		{[]string{"tank/data/*"}, "tank/data", true},
		{[]string{"tank/data/*"}, "tank/other", false},
		{[]string{"tank/*/vm-*"}, "tank/a", true},
		{[]string{"tank/*/vm-*"}, "tank/a/vm-1", false},
		{[]string{"vault/*"}, "tank", false},
		{[]string{"t*/*/*"}, "tank/x", true},
		{[]string{"tank", "tank/a/b"}, "tank/a", true},
		{nil, "tank", false},
		{[]string{"[bad"}, "tank", false},
	} {
		assert.Equal(t, tt.want, couldMatchBelow(tt.include, tt.name), "%v %q", tt.include, tt.name)
	}
}

func listCalls(calls []string) []string {
	var kids []string
	for _, c := range calls {
		if strings.HasPrefix(c, "kids:") {
			kids = append(kids, strings.TrimPrefix(c, "kids:"))
		}
	}
	return kids
}

func TestTheWalkListsOnlyTheParentsThatCanHoldAMatch(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/data/*"}
	api := tankAPI(t)
	api.kids = map[string][]string{
		"tank":       {"tank/data", "tank/other"},
		"tank/data":  {"tank/data/a", "tank/data/b"},
		"tank/other": {"tank/other/z"},
	}
	for _, n := range []string{"tank/data/a", "tank/data/b", "tank/data", "tank/other", "tank/other/z"} {
		api.objs[n] = propsNV(t, goodDatasetProps())
	}
	s, _ := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	assert.Equal(t, []string{"tank", "tank/data"}, listCalls(api.calls))
	var names []string
	for _, d := range r.datasets {
		names = append(names, d.name)
	}
	assert.Equal(t, []string{"tank/data/a", "tank/data/b"}, names)
}

func TestARootOnlyIncludeListsNoChildren(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank"}
	api := tankAPI(t)
	s, _ := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	assert.Empty(t, listCalls(api.calls))
	assert.Len(t, r.datasets, 1)
}

func TestADatasetTreeDeeperThanTheLimitStopsTheWalkWithOneWarning(t *testing.T) {
	cfg := spaceConfig()
	// A glob this deep keeps the walk going below the limit.
	cfg.DatasetInclude = []string{"tank/" + strings.Repeat("*/", maxDatasetDepth+8) + "*"}
	api := tankAPI(t)
	parent := "tank"
	for i := 0; i < maxDatasetDepth+5; i++ {
		child := fmt.Sprintf("%s/d%d", parent, i)
		api.kids[parent] = []string{child}
		api.objs[child] = propsNV(t, goodDatasetProps())
		parent = child
	}
	s, logs := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	assert.False(t, r.datasetOK)
	assert.LessOrEqual(t, len(r.datasets), maxDatasetDepth+1)
	require.Len(t, warnings(logs), 1)
	assert.Contains(t, warnings(logs)[0].Message, "deeper")
}

func TestAScrapeCarriesTheKstatSeriesAndTheSpaceSeriesTogether(t *testing.T) {
	cfg := hostConfig()
	cfg.DatasetInclude = []string{"tank", "tank/*"}
	s, _ := newTestScraper(cfg)
	s.space, _ = newTestSpace(cfg, tankAPI(t))

	md := scrapeOnce(t, s)

	names := metricNames(md)
	assert.Contains(t, names, "zfs.pool.io.bytes")
	assert.Contains(t, names, "zfs.pool.size")
	assert.Contains(t, names, "zfs.dataset.space.used")
	assert.Contains(t, names, "zfs.collector.success")
}

func TestAScrapeOnAHostWithNoPoolsDoesNotCallTheDevice(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = t.TempDir()
	s, _ := newTestScraper(cfg)
	api := tankAPI(t)
	s.space, _ = newTestSpace(cfg, api)

	md := scrapeOnce(t, s)

	assert.Equal(t, 0, md.ResourceMetrics().Len())
	assert.Empty(t, api.calls)
}

func TestAStuckDeviceKeepsTheKstatSeriesAndWarnsOnce(t *testing.T) {
	cfg := hostConfig()
	s, logs := newTestScraper(cfg)
	api := tankAPI(t)
	api.block = make(chan struct{})
	defer close(api.block)
	s.space, _ = newTestSpace(cfg, api)
	s.space.timeout = 20 * time.Millisecond

	first := scrapeOnce(t, s)
	second := scrapeOnce(t, s)

	assert.Contains(t, metricNames(first), "zfs.pool.io.bytes")
	assert.Contains(t, metricNames(second), "zfs.pool.io.bytes")
	assert.NotContains(t, metricNames(first), "zfs.pool.size")
	count := 0
	for _, w := range warnings(logs) {
		if strings.Contains(w.Message, "space series skipped") {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestTheStatfsRootPointsGoWhenTheIoctlReadGaveTheRootDataset(t *testing.T) {
	pools := []poolStats{
		{name: "tank", space: &spaceStats{used: 1, available: 2}},
		{name: "vault", space: &spaceStats{used: 3, available: 4}},
	}
	r := &spaceResult{datasets: []datasetSpace{{pool: "tank", name: "tank"}, {pool: "vault", name: "vault/x"}}}

	dropCoveredStatfs(pools, r)

	assert.Nil(t, pools[0].space)
	assert.NotNil(t, pools[1].space)
}

func TestTheStatfsRootPointsStayWithoutAnIoctlResult(t *testing.T) {
	pools := []poolStats{{name: "tank", space: &spaceStats{used: 1}}}

	dropCoveredStatfs(pools, nil)

	assert.NotNil(t, pools[0].space)
}

func TestClosingTheScraperClosesTheDevice(t *testing.T) {
	dev := &fakeDev{handler: func(vec uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return fill(dst, newNV().bytes()), nil
	}}
	cfg := hostConfig()
	s, _ := newTestScraper(cfg)
	s.space = newSpaceSource(s.episodes.logger, cfg, func(string) (zfsDevice, error) { return dev, nil })
	s.space.read([]string{"tank"})

	s.close()

	assert.Equal(t, 1, dev.closed)
}

func TestClosingAScraperWithNoSpaceSourceIsFine(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	assert.NotPanics(t, s.close)
}

func TestTheDefaultConfigReadsTheSpaceSeriesFromTheStandardNode(t *testing.T) {
	assert.Equal(t, "/dev/zfs", createDefaultConfig().(*Config).IoctlDevice)
}

func TestAnEmptyIoctlDeviceBuildsNoSpaceSource(t *testing.T) {
	cfg := hostConfig()
	cfg.IoctlDevice = ""

	s, _ := newTestScraper(cfg)

	assert.Nil(t, s.space)
}

func TestADefaultConfigBuildsASpaceSource(t *testing.T) {
	cfg := hostConfig()
	cfg.IoctlDevice = "/dev/zfs"

	s, _ := newTestScraper(cfg)

	assert.NotNil(t, s.space)
}
