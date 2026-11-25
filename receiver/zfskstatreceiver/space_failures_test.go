// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var (
	poolCollector    = map[string]string{"collector": "pool"}
	datasetCollector = map[string]string{"collector": "dataset"}
)

func twoPoolAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := tankAPI(t)
	api.props["vault"] = propsNV(t, goodPoolProps())
	return api
}

func TestAFailedPoolIsCountedWithOneWarningAndTheOtherPoolsStillEmit(t *testing.T) {
	api := twoPoolAPI(t)
	api.errs["props:tank"] = syscall.EIO
	s, logs := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank", "vault"}))
	emitTo(s.read([]string{"tank", "vault"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
	assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", poolAttr("vault")))
	require.Len(t, warnings(logs), 1, "one warning for the episode, not one for each scrape")
}

func TestAFailureIsCountedInEveryScrapeOfItsEpisode(t *testing.T) {
	api := twoPoolAPI(t)
	api.errs["props:tank"] = syscall.EIO
	s, _ := newTestSpace(spaceConfig(), api)

	s.read([]string{"tank", "vault"})
	md := emitTo(s.read([]string{"tank", "vault"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
}

func TestAFailedPoolStatisticsReadIsCountedAndTheSpaceSeriesStay(t *testing.T) {
	api := tankAPI(t)
	api.errs["stats:tank"] = syscall.EIO
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
	assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", poolAttr("tank")))
}

func TestAFailedDatasetIsCountedAndTheOthersStillEmit(t *testing.T) {
	api := tankAPI(t)
	api.errs["objset:tank"] = syscall.EIO
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", datasetCollector))
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.objects", datasetCollector))
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
}

func TestAChildListFailureIsCountedOnceForTheParent(t *testing.T) {
	api := tankAPI(t)
	api.objs["tank/b"] = propsNV(t, goodDatasetProps())
	api.kids["tank"] = []string{"tank/data", "tank/b"}
	api.errs["kidsAfter1:tank"] = syscall.EIO
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", datasetCollector))
}

func TestObjectsGoneBetweenTheListAndTheReadAreNotCounted(t *testing.T) {
	api := twoPoolAPI(t)
	api.errs["props:tank"] = syscall.ENOENT
	api.errs["objset:vault"] = syscall.ENOENT
	s, _ := newTestSpace(spaceConfig(), api)

	md := emitTo(s.read([]string{"tank", "vault"}))

	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", datasetCollector))
}

func TestAHealthyReadEmitsAZeroFailureCountForBothCollectors(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	md := emitTo(s.read([]string{"tank"}))

	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", datasetCollector))
}

func TestTheFailureCountIsAGaugeOfFailures(t *testing.T) {
	s, _ := newTestSpace(spaceConfig(), tankAPI(t))

	m := findMetric(t, emitTo(s.read([]string{"tank"})), "zfs.collector.failures")

	assert.Equal(t, "{failure}", m.Unit())
	assert.Equal(t, pmetric.MetricTypeGauge, m.Type())
}
