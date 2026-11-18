// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"math"
	"strings"
	"syscall"
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

// fakeAPI is a manual stand-in for the client; errs is keyed "props:<pool>", "stats:<pool>",
// "objset:<name>", "kids:<parent>" and "kidsAfter1:<parent>" (fails after the first child).
type fakeAPI struct {
	props map[string]*nvList
	stats map[string]*nvList
	objs  map[string]*nvList
	kids  map[string][]string
	errs  map[string]error
	calls []string
	block chan struct{}
}

func (f *fakeAPI) poolProps(pool string) (*nvList, error) {
	f.calls = append(f.calls, "props:"+pool)
	if f.block != nil {
		<-f.block
	}
	return f.props[pool], f.errs["props:"+pool]
}

func (f *fakeAPI) poolStats(pool string) (*nvList, error) {
	f.calls = append(f.calls, "stats:"+pool)
	return f.stats[pool], f.errs["stats:"+pool]
}

func (f *fakeAPI) objsetStats(name string) (*nvList, error) {
	f.calls = append(f.calls, "objset:"+name)
	return f.objs[name], f.errs["objset:"+name]
}

func (f *fakeAPI) children(parent string, fn func(string, func() (*nvList, error)) bool) error {
	f.calls = append(f.calls, "kids:"+parent)
	if err := f.errs["kids:"+parent]; err != nil {
		return err
	}
	for i, name := range f.kids[parent] {
		props := f.objs[name]
		decode := func() (*nvList, error) {
			f.calls = append(f.calls, "decode:"+name)
			return props, f.errs["decode:"+name]
		}
		if !fn(name, decode) {
			return nil
		}
		if i == 0 && f.errs["kidsAfter1:"+parent] != nil {
			return f.errs["kidsAfter1:"+parent]
		}
	}
	return nil
}

func (f *fakeAPI) close() error { return nil }

func mustNV(t *testing.T, e *nvEnc) *nvList {
	t.Helper()
	l, err := decodeNVList(e.bytes())
	require.NoError(t, err)
	return l
}

// propsNV packs properties the way the kernel does: each name holds a list with its value.
func propsNV(t *testing.T, props map[string]uint64) *nvList {
	t.Helper()
	e := newNV()
	for name, v := range props {
		e.list(name, func(i *nvEnc) { i.u64("source", 0).u64("value", v) })
	}
	return mustNV(t, e)
}

func goodPoolProps() map[string]uint64 {
	return map[string]uint64{
		"size": 1000, "allocated": 400, "free": 600, "freeing": 5, "leaked": 2,
		"fragmentation": 54, "capacity": 40, "dedupratio": 150, "health": 7, "readonly": 0,
	}
}

func goodDatasetProps() map[string]uint64 {
	return map[string]uint64{
		"type": 1, "used": 100, "available": 900, "quota": 0, "refquota": 500, "referenced": 60, "written": 7,
		"logicalused": 120, "usedbydataset": 60, "usedbysnapshots": 10, "usedbychildren": 30,
		"reservation": 1, "refreservation": 2, "compressratio": 175,
	}
}

func spaceConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.KstatPath = "testdata/host"
	cfg.DatasetInclude = []string{"tank", "tank/*"}
	return cfg
}

func newTestSpace(cfg *Config, api *fakeAPI) (*spaceSource, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	s := newSpaceSource(zap.New(core), cfg, nil)
	s.api = api
	return s, logs
}

func tankAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return &fakeAPI{
		props: map[string]*nvList{"tank": propsNV(t, goodPoolProps())},
		stats: map[string]*nvList{"tank": mustNV(t, newNV().list("vdev_tree", func(e *nvEnc) {
			e.u64s("vdev_stats", make([]uint64, 40)...)
		}))},
		objs: map[string]*nvList{
			"tank":      propsNV(t, goodDatasetProps()),
			"tank/data": propsNV(t, goodDatasetProps()),
		},
		kids: map[string][]string{"tank": {"tank/data"}},
		errs: map[string]error{},
	}
}

func emitTo(r *spaceResult) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	emitIoctlSpace(sm, r, newResetTracker(), 0, pcommon.Timestamp(5))
	return md
}

func gaugeInt(t *testing.T, md pmetric.Metrics, name string, attrs map[string]string) int64 {
	t.Helper()
	return pointFor(t, findMetric(t, md, name), attrs).IntValue()
}

func gaugeFloat(t *testing.T, md pmetric.Metrics, name string, attrs map[string]string) float64 {
	t.Helper()
	return pointFor(t, findMetric(t, md, name), attrs).DoubleValue()
}

func poolAttr(pool string) map[string]string { return map[string]string{"zfs.pool": pool} }

func TestPoolSeriesCarryTheZpoolListValues(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	tank := poolAttr("tank")
	assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", tank))
	assert.Equal(t, int64(400), gaugeInt(t, md, "zfs.pool.allocated", tank))
	assert.Equal(t, int64(600), gaugeInt(t, md, "zfs.pool.free", tank))
	assert.Equal(t, int64(5), gaugeInt(t, md, "zfs.pool.freeing", tank))
	assert.Equal(t, int64(2), gaugeInt(t, md, "zfs.pool.leaked", tank))
	assert.InDelta(t, 0.54, gaugeFloat(t, md, "zfs.pool.fragmentation", tank), 1e-9)
	assert.InDelta(t, 0.4, gaugeFloat(t, md, "zfs.pool.capacity", tank), 1e-9)
	assert.InDelta(t, 1.5, gaugeFloat(t, md, "zfs.pool.dedup.ratio", tank), 1e-9)
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.pool.health", tank))
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.pool.readonly", tank))
}

func TestPoolMetricUnitsAndTypes(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	for name, unit := range map[string]string{
		"zfs.pool.size": "By", "zfs.pool.allocated": "By", "zfs.pool.free": "By", "zfs.pool.freeing": "By",
		"zfs.pool.leaked": "By", "zfs.pool.fragmentation": "1", "zfs.pool.capacity": "1",
		"zfs.pool.dedup.ratio": "1", "zfs.pool.health": "1", "zfs.pool.readonly": "1",
		"zfs.collector.success": "1", "zfs.collector.duration": "s", "zfs.collector.objects": "{object}",
	} {
		m := findMetric(t, md, name)
		assert.Equal(t, unit, m.Unit(), name)
		assert.Equal(t, pmetric.MetricTypeGauge, m.Type(), name)
	}
}

func TestCapacityIsZeroForAPoolWithNoSize(t *testing.T) {
	api := tankAPI(t)
	api.props["tank"] = propsNV(t, map[string]uint64{"size": 0, "allocated": 0, "health": 7})
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Zero(t, gaugeFloat(t, md, "zfs.pool.capacity", poolAttr("tank")))
}

func TestUnknownFragmentationGivesNoPoint(t *testing.T) {
	api := tankAPI(t)
	p := goodPoolProps()
	p["fragmentation"] = math.MaxUint64
	api.props["tank"] = propsNV(t, p)
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.NotContains(t, metricNames(md), "zfs.pool.fragmentation")
}

func TestAMissingPoolPropertyGivesNoPointForIt(t *testing.T) {
	api := tankAPI(t)
	api.props["tank"] = propsNV(t, map[string]uint64{"size": 10, "allocated": 4, "health": 7})
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	names := metricNames(md)
	assert.Contains(t, names, "zfs.pool.size")
	assert.NotContains(t, names, "zfs.pool.leaked")
	assert.NotContains(t, names, "zfs.pool.readonly")
	assert.Empty(t, warnings(logs))
}

func TestHealthCodesFollowTheDocumentedTable(t *testing.T) {
	for state, want := range map[uint64]int64{7: 0, 6: 1, 5: 2, 2: 3, 4: 4, 3: 5} {
		api := tankAPI(t)
		p := goodPoolProps()
		p["health"] = state
		api.props["tank"] = propsNV(t, p)
		s, _ := newTestSpace(spaceConfig(), api)

		md := emitTo(s.read([]string{"tank"}))

		assert.Equal(t, want, gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")), "vdev state %d", state)
	}
}

func TestAHealthStateOutsideTheTableGivesNoPointAndOneWarningPerEpisode(t *testing.T) {
	api := tankAPI(t)
	p := goodPoolProps()
	p["health"] = 99
	api.props["tank"] = propsNV(t, p)
	s, logs := newTestSpace(spaceConfig(), api)

	first := emitTo(s.read([]string{"tank"}))
	s.read([]string{"tank"})

	assert.NotContains(t, metricNames(first), "zfs.pool.health")
	assert.Contains(t, metricNames(first), "zfs.pool.size")
	require.Len(t, warnings(logs), 1)
	assert.Contains(t, warnings(logs)[0].Message, "health")
	assert.Contains(t, warnings(logs)[0].ContextMap()["state"], "99")
}

func vdevTree(t *testing.T, leaves ...[]uint64) *nvList {
	t.Helper()
	root := make([]uint64, 40)
	return mustNV(t, newNV().list("vdev_tree", func(e *nvEnc) {
		e.u64s("vdev_stats", root...)
		fns := make([]func(*nvEnc), len(leaves))
		for i, stats := range leaves {
			fns[i] = func(c *nvEnc) { c.u64s("vdev_stats", stats...) }
		}
		e.lists("children", fns...)
	}))
}

func leafStats(read, write, sum uint64) []uint64 {
	s := make([]uint64, 40)
	s[20], s[21], s[22] = read, write, sum
	return s
}

func TestVdevErrorsSumTheLeafVdevsByType(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = vdevTree(t, leafStats(1, 2, 3), leafStats(10, 20, 30))
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	m := findMetric(t, md, "zfs.pool.vdev.errors")
	assert.Equal(t, "{error}", m.Unit())
	assert.Equal(t, pmetric.MetricTypeSum, m.Type())
	assert.True(t, m.Sum().IsMonotonic())
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.Sum().AggregationTemporality())
	for typ, want := range map[string]int64{"read": 11, "write": 22, "checksum": 33} {
		assert.Equal(t, want, pointFor(t, m, map[string]string{"zfs.pool": "tank", "error.type": typ}).IntValue(), typ)
	}
}

func TestVdevErrorsAreAbsentWithoutVdevStatsAndRaiseNoWarning(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = mustNV(t, newNV().str("name", "tank"))
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.NotContains(t, metricNames(md), "zfs.pool.vdev.errors")
	assert.NotContains(t, metricNames(md), "zfs.pool.scrub.state")
	assert.Empty(t, warnings(logs))
}

func TestVdevStatsTooShortForTheErrorCountersAreIgnored(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = vdevTree(t, []uint64{1, 2, 3})
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.NotContains(t, metricNames(md), "zfs.pool.vdev.errors")
	assert.Empty(t, warnings(logs))
}

func TestScrubStateComesFromTheScanStatsInTheTreeOrAtTheTop(t *testing.T) {
	inTree := mustNV(t, newNV().list("vdev_tree", func(e *nvEnc) { e.u64s("scan_stats", 1, 2, 0) }))
	atTop := mustNV(t, newNV().u64s("scan_stats", 1, 3, 0))

	for want, stats := range map[int64]*nvList{2: inTree, 3: atTop} {
		api := tankAPI(t)
		api.stats["tank"] = stats
		s, _ := newTestSpace(spaceConfig(), api)

		md := emitTo(s.read([]string{"tank"}))

		assert.Equal(t, want, gaugeInt(t, md, "zfs.pool.scrub.state", poolAttr("tank")))
	}
}

func TestAFailedStatsReadKeepsThePoolSpaceSeries(t *testing.T) {
	api := tankAPI(t)
	api.errs["stats:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))
	s.read([]string{"tank"})

	assert.Contains(t, metricNames(md), "zfs.pool.size")
	assert.NotContains(t, metricNames(md), "zfs.pool.vdev.errors")
	require.Len(t, warnings(logs), 1)
	assert.Contains(t, warnings(logs)[0].Message, "statistics")
}

func TestAPoolGoneBeforeItsStatisticsIsQuiet(t *testing.T) {
	api := tankAPI(t)
	api.errs["stats:tank"] = syscall.ENOENT
	s, logs := newTestSpace(spaceConfig(), api)

	s.read([]string{"tank"})

	assert.Empty(t, warnings(logs))
}

func TestDatasetSeriesCarryTheZfsListValues(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	attrs := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/data", "zfs.dataset.type": "filesystem"}
	for name, want := range map[string]int64{
		"zfs.dataset.space.used": 100, "zfs.dataset.space.available": 900, "zfs.dataset.space.quota": 0,
		"zfs.dataset.space.refquota": 500, "zfs.dataset.space.referenced": 60, "zfs.dataset.space.written": 7,
		"zfs.dataset.space.logical_used": 120, "zfs.dataset.space.used_by_dataset": 60,
		"zfs.dataset.space.used_by_snapshots": 10, "zfs.dataset.space.used_by_children": 30,
		"zfs.dataset.space.reservation": 1, "zfs.dataset.space.refreservation": 2,
	} {
		m := findMetric(t, md, name)
		assert.Equal(t, "By", m.Unit(), name)
		assert.Equal(t, want, pointFor(t, m, attrs).IntValue(), name)
	}
	assert.InDelta(t, 1.75, gaugeFloat(t, md, "zfs.dataset.compression.ratio", attrs), 1e-9)
	assert.NotContains(t, metricNames(md), "zfs.dataset.volume.size")
}

func TestTheRootDatasetIsReadByItsPoolName(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	root := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank", "zfs.dataset.type": "filesystem"}
	assert.Equal(t, int64(100), gaugeInt(t, md, "zfs.dataset.space.used", root))
}

func TestAQuotaOfZeroIsEmittedAsZero(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	attrs := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/data"}
	assert.Zero(t, gaugeInt(t, md, "zfs.dataset.space.quota", attrs))
}

func TestAVolumeGivesItsSizeAndTheVolumeType(t *testing.T) {
	api := tankAPI(t)
	vol := map[string]uint64{"type": 4, "used": 8, "available": 9, "volsize": 12884901888}
	api.objs["tank/vm-1"] = propsNV(t, vol)
	api.kids["tank"] = []string{"tank/data", "tank/vm-1"}
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	attrs := map[string]string{"zfs.pool": "tank", "zfs.dataset": "tank/vm-1", "zfs.dataset.type": "volume"}
	assert.Equal(t, int64(12884901888), gaugeInt(t, md, "zfs.dataset.volume.size", attrs))
	assert.Equal(t, int64(8), gaugeInt(t, md, "zfs.dataset.space.used", attrs))
	fs := map[string]string{"zfs.dataset": "tank/data"}
	for _, dp := range allPoints(findMetric(t, md, "zfs.dataset.volume.size")) {
		v, _ := dp.Attributes().Get("zfs.dataset")
		assert.NotEqual(t, fs["zfs.dataset"], v.Str())
	}
}

func allPoints(m pmetric.Metric) []pmetric.NumberDataPoint {
	dps := dataPoints(m)
	out := make([]pmetric.NumberDataPoint, dps.Len())
	for i := range out {
		out[i] = dps.At(i)
	}
	return out
}

func TestAnObjectWithAVolsizeButNoTypeIsAVolume(t *testing.T) {
	api := tankAPI(t)
	api.objs["tank/vm-2"] = propsNV(t, map[string]uint64{"used": 1, "volsize": 4096})
	api.kids["tank"] = []string{"tank/vm-2"}
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	attrs := map[string]string{"zfs.dataset": "tank/vm-2", "zfs.dataset.type": "volume"}
	assert.Equal(t, int64(4096), gaugeInt(t, md, "zfs.dataset.volume.size", attrs))
}

func TestWithNoIncludeNoDatasetIsReadAndNoDatasetSeriesOrCollectorIsEmitted(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = nil
	api := tankAPI(t)
	s, _ := newTestSpace(cfg, api)

	md := emitTo(s.read([]string{"tank"}))

	for _, n := range metricNames(md) {
		assert.False(t, strings.HasPrefix(n, "zfs.dataset."), n)
	}
	for _, c := range api.calls {
		assert.False(t, strings.HasPrefix(c, "objset:") || strings.HasPrefix(c, "kids:"), c)
	}
	m := findMetric(t, md, "zfs.collector.success")
	assert.Equal(t, 1, dataPoints(m).Len())
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.success", map[string]string{"collector": "pool"}))
}

func TestIncludeAndExcludeGlobsSelectTheDatasets(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/*"}
	cfg.DatasetExclude = []string{"tank/tmp"}
	api := tankAPI(t)
	api.objs["tank/tmp"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/data", "tank/tmp"}
	s, _ := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	var names []string
	for _, d := range r.datasets {
		names = append(names, d.name)
	}
	assert.Equal(t, []string{"tank/data"}, names)
}

func TestSelectionDescendsThroughDatasetsTheGlobsSkip(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/a/b"}
	api := tankAPI(t)
	api.objs["tank/a"] = propsNV(t, goodDatasetProps())
	api.objs["tank/a/b"] = propsNV(t, goodDatasetProps())
	api.kids = map[string][]string{"tank": {"tank/a"}, "tank/a": {"tank/a/b"}}
	s, _ := newTestSpace(cfg, api)

	r := s.read([]string{"tank"})

	require.Len(t, r.datasets, 1)
	assert.Equal(t, "tank/a/b", r.datasets[0].name)
}

func TestMaxDatasetsKeepsTheFirstInSortedOrderWithOneWarning(t *testing.T) {
	cfg := spaceConfig()
	cfg.DatasetInclude = []string{"tank/*"}
	cfg.MaxDatasets = 2
	api := tankAPI(t)
	for _, n := range []string{"tank/c", "tank/a", "tank/b"} {
		api.objs[n] = propsNV(t, goodDatasetProps())
	}
	api.kids["tank"] = []string{"tank/c", "tank/a", "tank/b"}
	s, logs := newTestSpace(cfg, api)

	first := s.read([]string{"tank"})
	s.read([]string{"tank"})

	var names []string
	for _, d := range first.datasets {
		names = append(names, d.name)
	}
	assert.Equal(t, []string{"tank/a", "tank/b"}, names)
	assert.Equal(t, 2, first.datasetObjs)
	require.Len(t, warnings(logs), 1)
	assert.Contains(t, warnings(logs)[0].Message, "max_datasets")
}

func TestAFailedDatasetIsSkippedWithOneWarningAndTheRestContinue(t *testing.T) {
	api := tankAPI(t)
	api.objs["tank/b"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/data", "tank/b"}
	api.errs["objset:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank"})
	s.read([]string{"tank"})

	var names []string
	for _, d := range r.datasets {
		names = append(names, d.name)
	}
	assert.Equal(t, []string{"tank/b", "tank/data"}, names)
	require.Len(t, warnings(logs), 1)
	assert.Contains(t, warnings(logs)[0].ContextMap()["zfs.dataset"], "tank")
}

func TestADatasetDestroyedBetweenListingAndReadIsSkippedQuietly(t *testing.T) {
	api := tankAPI(t)
	api.errs["objset:tank"] = syscall.ENOENT
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank"})

	require.Len(t, r.datasets, 1)
	assert.Empty(t, warnings(logs))
}

func TestAListFailureMidWalkKeepsWhatWasReadAndMarksTheDatasetCollectorFailed(t *testing.T) {
	api := tankAPI(t)
	api.objs["tank/b"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/data", "tank/b"}
	api.errs["kidsAfter1:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank"})

	assert.Len(t, r.datasets, 2, "the root and the first child")
	assert.False(t, r.datasetOK)
	assert.True(t, r.poolOK)
	assert.Len(t, warnings(logs), 1)
}

func TestAFailedPoolIsSkippedAndTheOtherPoolStaysWhileThePoolCollectorStaysUp(t *testing.T) {
	api := tankAPI(t)
	api.props["vault"] = propsNV(t, goodPoolProps())
	api.errs["props:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank", "vault"})

	require.Len(t, r.pools, 1)
	assert.Equal(t, "vault", r.pools[0].name)
	assert.True(t, r.poolOK)
	assert.Equal(t, 1, r.poolObjs)
	assert.Len(t, warnings(logs), 1)
}

func TestWhenEveryPoolFailsThePoolCollectorReportsFailure(t *testing.T) {
	api := tankAPI(t)
	api.errs["props:tank"] = syscall.EIO
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.success", map[string]string{"collector": "pool"}))
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.objects", map[string]string{"collector": "pool"}))
}

func TestAPoolExportedBetweenTheListAndTheReadIsSkippedQuietly(t *testing.T) {
	api := tankAPI(t)
	api.errs["props:tank"] = syscall.ENOENT
	s, logs := newTestSpace(spaceConfig(), api)

	r := s.read([]string{"tank"})

	assert.Empty(t, r.pools)
	assert.Empty(t, warnings(logs))
}

func TestCollectorSeriesReportSuccessDurationAndObjectsForBothCollectors(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))
	times := []time.Time{time.Unix(100, 0), time.Unix(102, 0), time.Unix(102, 0), time.Unix(105, 0)}
	s.now = func() time.Time { t0 := times[0]; times = times[1:]; return t0 }

	md := emitTo(s.read([]string{"tank"}))

	pool, ds := map[string]string{"collector": "pool"}, map[string]string{"collector": "dataset"}
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.success", pool))
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.success", ds))
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.objects", pool))
	assert.Equal(t, int64(2), gaugeInt(t, md, "zfs.collector.objects", ds))
	assert.InDelta(t, 2.0, gaugeFloat(t, md, "zfs.collector.duration", pool), 1e-9)
	assert.InDelta(t, 3.0, gaugeFloat(t, md, "zfs.collector.duration", ds), 1e-9)
}

func TestOpenFailureGivesFailureSeriesAndOneWarningThatNamesTheNode(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	opens := 0
	open := func(string) (zfsDevice, error) { opens++; return nil, syscall.EACCES }
	s := newSpaceSource(zap.New(core), spaceConfig(), open)

	r := s.read([]string{"tank"})
	s.read([]string{"tank"})

	md := emitTo(r)
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.success", map[string]string{"collector": "pool"}))
	assert.NotContains(t, metricNames(md), "zfs.pool.size")
	assert.Equal(t, 2, opens, "the open is tried again each scrape")
	require.Len(t, warnings(logs), 1)
	assert.Equal(t, "/dev/zfs", warnings(logs)[0].ContextMap()["device"])
}

func TestAnUnsupportedSystemIsQuiet(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	s := newSpaceSource(zap.New(core), spaceConfig(), func(string) (zfsDevice, error) { return nil, errIoctlUnsupported })

	r := s.read([]string{"tank"})

	assert.False(t, r.isSupported)
	assert.Empty(t, warnings(logs))
}

func TestTheDeviceOpensOnceAndIsReusedAndClosedOnClose(t *testing.T) {
	dev := &fakeDev{handler: func(vec uint, _ string, _ uint64, dst []byte) (callReply, error) {
		if vec == vecDatasetListNext {
			return callReply{}, syscall.ESRCH
		}
		return fill(dst, newNV().bytes()), nil
	}}
	opens := 0
	s := newSpaceSource(zap.NewNop(), spaceConfig(), func(string) (zfsDevice, error) { opens++; return dev, nil })

	s.read([]string{"tank"})
	s.read([]string{"tank"})
	s.close()

	assert.Equal(t, 1, opens)
	assert.Equal(t, 1, dev.closed)
}

func TestScanReturnsTheResultWhenTheReadIsQuick(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	r, err := s.scan([]string{"tank"})

	require.NoError(t, err)
	assert.Len(t, r.pools, 1)
}

func TestAStuckReadTimesOutAndNoSecondReadStartsWhileItRuns(t *testing.T) {
	api := tankAPI(t)
	api.block = make(chan struct{})
	s, _ := newTestSpace(spaceConfig(), api)
	s.timeout = 20 * time.Millisecond

	_, err := s.scan([]string{"tank"})
	_, again := s.scan([]string{"tank"})

	assert.ErrorIs(t, err, errSpaceTimeout)
	assert.ErrorIs(t, again, errSpaceBusy)
	close(api.block)
	assert.Eventually(t, func() bool { _, err := s.scan([]string{"tank"}); return err == nil }, time.Second, 10*time.Millisecond)
}

func TestVdevErrorStartTimeMovesWhenACounterGoesDown(t *testing.T) {
	api := tankAPI(t)
	s, _ := newTestSpace(spaceConfig(), api)
	tr := newResetTracker()
	attrs := map[string]string{"zfs.pool": "tank", "error.type": "read"}
	scrape := func(read uint64, now pcommon.Timestamp) pmetric.Metrics {
		api.stats["tank"] = vdevTree(t, leafStats(read, 0, 0))
		md := pmetric.NewMetrics()
		sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
		emitIoctlSpace(sm, s.read([]string{"tank"}), tr, 1, now)
		tr.endScrape()
		return md
	}

	first := scrape(5, 10)
	second := scrape(7, 20)
	third := scrape(2, 30)

	start := func(md pmetric.Metrics) pcommon.Timestamp {
		return pointFor(t, findMetric(t, md, "zfs.pool.vdev.errors"), attrs).StartTimestamp()
	}
	assert.Equal(t, pcommon.Timestamp(1), start(first))
	assert.Equal(t, pcommon.Timestamp(1), start(second))
	assert.Equal(t, pcommon.Timestamp(30), start(third))
}

func TestAStatsErrnoSkipsTheVdevSeriesWarnsOnceAndKeepsSuspended(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = mustNV(t, newNV().u64("suspended", 1).list("vdev_tree", func(e *nvEnc) {
		e.u64s("vdev_stats", leafStats(5, 5, 5)...)
	}))
	api.errs["stats:tank"] = syscall.EAGAIN
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))
	s.read([]string{"tank"})

	assert.NotContains(t, metricNames(md), "zfs.pool.vdev.errors")
	assert.Equal(t, int64(6), gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")))
	assert.Len(t, warnings(logs), 1)
}

func TestAStatsErrnoOfAGonePoolIsQuiet(t *testing.T) {
	api := tankAPI(t)
	api.errs["stats:tank"] = syscall.ENOENT
	s, logs := newTestSpace(spaceConfig(), api)

	s.read([]string{"tank"})

	assert.Empty(t, warnings(logs))
}

func TestASlowReadHandsItsResultToTheNextScrape(t *testing.T) {
	api := tankAPI(t)
	api.block = make(chan struct{})
	s, _ := newTestSpace(spaceConfig(), api)
	s.timeout = 20 * time.Millisecond

	_, first := s.scan([]string{"tank"})
	_, second := s.scan([]string{"tank"})
	close(api.block)
	var late *spaceResult
	require.Eventually(t, func() bool {
		r, err := s.scan([]string{"tank"})
		late = r
		return err == nil
	}, time.Second, 10*time.Millisecond)

	assert.ErrorIs(t, first, errSpaceTimeout)
	assert.ErrorIs(t, second, errSpaceBusy)
	assert.Contains(t, first.Error(), "slower than")
	require.NotNil(t, late)
	assert.Len(t, late.pools, 1, "the read that ran long gave this result")
}

func TestAReadAfterTheLateResultStartsFresh(t *testing.T) {
	api := tankAPI(t)
	api.block = make(chan struct{})
	s, _ := newTestSpace(spaceConfig(), api)
	s.timeout = 20 * time.Millisecond
	_, _ = s.scan([]string{"tank"})
	close(api.block)
	require.Eventually(t, func() bool { _, err := s.scan([]string{"tank"}); return err == nil }, time.Second, 10*time.Millisecond)

	r, err := s.scan([]string{"tank"})

	require.NoError(t, err)
	assert.Len(t, r.pools, 1)
}

func TestADeviceThatWentAwayIsClosedAndOpenedAgain(t *testing.T) {
	calls := 0
	dev := &fakeDev{handler: func(vec uint, _ string, _ uint64, dst []byte) (callReply, error) {
		calls++
		if calls == 1 {
			return callReply{}, syscall.ENXIO
		}
		if vec == vecDatasetListNext {
			return callReply{}, syscall.ESRCH
		}
		return fill(dst, newNV().bytes()), nil
	}}
	opens := 0
	s := newSpaceSource(zap.NewNop(), spaceConfig(), func(string) (zfsDevice, error) { opens++; return dev, nil })

	first := s.read([]string{"tank"})
	second := s.read([]string{"tank"})

	assert.Equal(t, 2, opens, "the second read opened the device again")
	assert.Equal(t, 1, dev.closed)
	assert.False(t, first.poolOK)
	assert.True(t, second.poolOK)
}

func TestOtherErrorsKeepTheDeviceOpen(t *testing.T) {
	dev := &fakeDev{handler: func(uint, string, uint64, []byte) (callReply, error) { return callReply{}, syscall.EIO }}
	opens := 0
	s := newSpaceSource(zap.NewNop(), spaceConfig(), func(string) (zfsDevice, error) { opens++; return dev, nil })

	s.read([]string{"tank"})
	s.read([]string{"tank"})

	assert.Equal(t, 1, opens)
	assert.Zero(t, dev.closed)
}

func TestTheReadWarnsAgainAfterTheFaultClears(t *testing.T) {
	api := tankAPI(t)
	api.errs["props:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	s.read([]string{"tank"})
	delete(api.errs, "props:tank")
	s.read([]string{"tank"})
	api.errs["props:tank"] = errors.New("again")
	s.read([]string{"tank"})

	assert.Len(t, warnings(logs), 2)
}
