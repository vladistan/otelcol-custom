// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestASuspendedPoolReportsHealthSix(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = mustNV(t, newNV().u64("suspended", 1).list("vdev_tree", func(e *nvEnc) {
		e.u64s("vdev_stats", make([]uint64, 40)...)
	}))
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(6), gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")))
}

func TestAPoolWithNoSuspendedKeyOrZeroKeepsItsVdevHealth(t *testing.T) {
	for name, stats := range map[string]*nvList{
		"zero":   mustNV(t, newNV().u64("suspended", 0)),
		"absent": mustNV(t, newNV().str("name", "tank")),
	} {
		api := tankAPI(t)
		api.stats["tank"] = stats
		s, _ := newTestSpace(spaceConfig(), api)

		md := emitTo(s.read([]string{"tank"}))

		assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")), name)
	}
}

func TestASuspendedPoolWithAnUnreadableHealthStateStillReportsSix(t *testing.T) {
	api := tankAPI(t)
	p := goodPoolProps()
	p["health"] = 99
	api.props["tank"] = propsNV(t, p)
	api.stats["tank"] = mustNV(t, newNV().u64("suspended", 2))
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(6), gaugeInt(t, md, "zfs.pool.health", poolAttr("tank")))
	assert.Empty(t, warnings(logs))
}

func TestTheHealthCodesMatchTheExporterTable(t *testing.T) {
	want := map[string]int64{
		"ONLINE": 0, "DEGRADED": 1, "FAULTED": 2, "OFFLINE": 3, "UNAVAIL": 4, "REMOVED": 5, "SUSPENDED": 6,
	}
	got := map[string]int64{"SUSPENDED": suspendedHealthCode}
	for _, h := range healthStates {
		got[h.name] = h.code
	}

	assert.Equal(t, want, got)
}

// rootStats is a pool reply whose root vdev reports a state aux code.
func rootStats(t *testing.T, aux uint64) *nvList {
	t.Helper()
	stats := make([]uint64, 40)
	stats[2] = aux
	return mustNV(t, newNV().list("vdev_tree", func(e *nvEnc) { e.u64s("vdev_stats", stats...) }))
}

func healthOf(t *testing.T, state uint64, stats *nvList) (code int64, ok bool, warned int) {
	t.Helper()
	api := tankAPI(t)
	p := goodPoolProps()
	p["health"] = state
	api.props["tank"] = propsNV(t, p)
	api.stats["tank"] = stats
	s, logs := newTestSpace(spaceConfig(), api)
	md := emitTo(s.read([]string{"tank"}))
	for _, n := range metricNames(md) {
		if n == "zfs.pool.health" {
			return gaugeInt(t, md, n, poolAttr("tank")), true, len(warnings(logs))
		}
	}
	return 0, false, len(warnings(logs))
}

func TestACantOpenRootIsFaultedForCorruptDataOrABadLogAndUnavailOtherwise(t *testing.T) {
	for aux, want := range map[uint64]int64{2: 2, 13: 2, 0: 4, 1: 4, 6: 4, 11: 4} {
		code, ok, warned := healthOf(t, 4, rootStats(t, aux))

		assert.True(t, ok, "aux %d", aux)
		assert.Equal(t, want, code, "aux %d", aux)
		assert.Zero(t, warned)
	}
}

func TestACantOpenRootWithoutItsStatsIsUnavail(t *testing.T) {
	code, ok, _ := healthOf(t, 4, mustNV(t, newNV().str("name", "tank")))

	assert.True(t, ok)
	assert.Equal(t, int64(4), code)
}

func TestAClosedRootIsOfflineAsInZpool(t *testing.T) {
	code, ok, _ := healthOf(t, 1, rootStats(t, 0))

	assert.True(t, ok)
	assert.Equal(t, int64(3), code)
}

func TestASplitPoolAndAnUnknownStateGiveNoHealthPointAndOneWarning(t *testing.T) {
	_, ok, warned := healthOf(t, 4, rootStats(t, 15))
	assert.False(t, ok, "SPLIT is not in the code table")
	assert.Equal(t, 1, warned)

	_, ok, warned = healthOf(t, 0, rootStats(t, 0))
	assert.False(t, ok, "UNKNOWN is not in the code table")
	assert.Equal(t, 1, warned)
}

func scanStats(t *testing.T, fn, state uint64) *nvList {
	t.Helper()
	return mustNV(t, newNV().list("vdev_tree", func(e *nvEnc) { e.u64s("scan_stats", fn, state, 0) }))
}

func TestAResilverIsReportedAsAResilverAndNotAsAScrub(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = scanStats(t, 2, 1)
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.pool.resilver.state", poolAttr("tank")))
	assert.NotContains(t, metricNames(md), "zfs.pool.scrub.state")
}

func TestAScrubAndAnIdlePoolAreReportedAsScrubState(t *testing.T) {
	for name, fn := range map[string]uint64{"scrub": 1, "none": 0} {
		api := tankAPI(t)
		api.stats["tank"] = scanStats(t, fn, 2)
		s, _ := newTestSpace(spaceConfig(), api)

		md := emitTo(s.read([]string{"tank"}))

		assert.Equal(t, int64(2), gaugeInt(t, md, "zfs.pool.scrub.state", poolAttr("tank")), name)
		assert.NotContains(t, metricNames(md), "zfs.pool.resilver.state", name)
	}
}

func TestAScanFunctionOutsideTheTableGivesNoScanSeries(t *testing.T) {
	api := tankAPI(t)
	api.stats["tank"] = scanStats(t, 9, 1)
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.NotContains(t, metricNames(md), "zfs.pool.scrub.state")
	assert.NotContains(t, metricNames(md), "zfs.pool.resilver.state")
	assert.Empty(t, warnings(logs))
}
