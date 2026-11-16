// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeBoth is a session source that also lists LUNs.
type fakeBoth struct {
	fakeSessions
	lunList  []lun
	lunErr   error
	lunReads atomic.Int32
}

func (f *fakeBoth) luns() ([]lun, error) {
	f.lunReads.Add(1)
	return f.lunList, f.lunErr
}

func lunSessions() []session {
	return []session{
		{id: 1, target: "iqn.a", tpgt: "1", state: "LOGGED_IN", portal: "192.0.2.10:3260",
			connections: []connection{{portal: "192.0.2.10:3260", state: "LOGGED_IN"}}},
		{id: 2, target: "iqn.a", tpgt: "1", state: "LOGGED_IN", portal: "192.0.2.11:3260",
			connections: []connection{{portal: "192.0.2.11:3260", state: "LOGGED_IN"}}},
	}
}

func newLUNScraper(logger *zap.Logger, src *fakeBoth, max int) *scraper {
	s := newScraper(logger, src)
	s.luns, s.maxLUNs = src, max
	return s
}

func hasMetric(md pmetric.Metrics, name string) bool {
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return true
		}
	}
	return false
}

func okLUN(session, id int, device string) lun {
	return lun{session: session, id: id, device: device, state: "running", sizeBytes: 1 << 20, hasSize: true}
}

func TestEachLUNHasAStatusASizeAndAnInfoPoint(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{
		{session: 1, id: 3, device: "sdb", holder: "dm-0", state: "running", sizeBytes: 1 << 30, hasSize: true},
	}}
	now := time.Unix(1700000000, 0)

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(now)

	status := metricNamed(t, md, "hw.status")
	assert.Equal(t, "1", status.Unit())
	dps := status.Gauge().DataPoints()
	require.Equal(t, 5, dps.Len(), "one point for each hw.state")
	byState := map[string]int64{}
	for i := 0; i < dps.Len(); i++ {
		a := attrs(dps.At(i))
		byState[a["hw.state"]] = dps.At(i).IntValue()
		assert.Equal(t, "iqn.a/192.0.2.10:3260/3", a["hw.id"])
		assert.Equal(t, "sdb", a["hw.name"])
		assert.Equal(t, "logical_disk", a["hw.type"])
		assert.Equal(t, "iqn.a", a["hw.parent"])
	}
	assert.Equal(t, map[string]int64{"ok": 1, "needs_cleaning": 0, "degraded": 0, "predicted_failure": 0, "failed": 0}, byState)

	limit := metricNamed(t, md, "hw.logical_disk.limit")
	assert.Equal(t, "By", limit.Unit())
	require.Equal(t, 1, limit.Gauge().DataPoints().Len())
	assert.Equal(t, int64(1<<30), limit.Gauge().DataPoints().At(0).IntValue())
	assert.Equal(t, "iqn.a/192.0.2.10:3260/3", attrs(limit.Gauge().DataPoints().At(0))["hw.id"])

	info := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 1, info.Len())
	assert.Equal(t, int64(1), info.At(0).IntValue())
	assert.Equal(t, map[string]string{
		"state": "running", "iscsi.target": "iqn.a", "iscsi.portal": "192.0.2.10:3260", "iscsi.lun": "3", "iscsi.device": "sdb", "iscsi.holder": "dm-0",
	}, attrs(info.At(0)))
}

func TestSeveralHoldersAreCountedOnTheInfoPoint(t *testing.T) {
	one := okLUN(1, 1, "sdb")
	one.holder, one.holders = "dm-0", 1
	many := okLUN(1, 2, "sdc")
	many.holder, many.holders = "dm-0", 3
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{one, many}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	info := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 2, info.Len())
	_, hasCount := attrs(info.At(0))["iscsi.holders"]
	assert.False(t, hasCount, "one holder is named and not counted")
	assert.Equal(t, "dm-0", attrs(info.At(1))["iscsi.holder"])
	assert.Equal(t, "3", attrs(info.At(1))["iscsi.holders"])
}

// blockingLUNs is a LUN source whose read waits for release.
type blockingLUNs struct {
	fakeSessions
	release chan struct{}
	started atomic.Int32
}

func (b *blockingLUNs) luns() ([]lun, error) {
	b.started.Add(1)
	<-b.release
	return []lun{okLUN(1, 1, "sdb")}, nil
}

func TestAReadOfTheLUNsThatNeverReturnsDoesNotHoldTheScrapeAndIsNotStartedAgain(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &blockingLUNs{fakeSessions: fakeSessions{list: lunSessions()}, release: make(chan struct{})}
	s := newScraper(zap.New(core), src)
	s.luns, s.maxLUNs, s.lunTimeout = src, 10, 20*time.Millisecond

	var md pmetric.Metrics
	for range 3 {
		md = s.scrape(time.Now())
	}

	assert.True(t, hasMetric(md, "iscsi.session.state"), "the session series are sent")
	assert.False(t, hasMetric(md, "iscsi.lun.state"))
	assert.Equal(t, int32(1), src.started.Load(), "one stuck read, never a second")
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.WarnLevel).Len())

	close(src.release)
	require.Eventually(t, func() bool { return !s.lunBusy.Load() }, 2*time.Second, 5*time.Millisecond)
	md = s.scrape(time.Now())
	assert.True(t, hasMetric(md, "iscsi.lun.state"), "the LUNs are read again once the read returned")
}

func TestTwoPathsToOneLUNAreTwoSeries(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{okLUN(1, 1, "sdb"), okLUN(2, 1, "sdc")}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	ids := map[string]bool{}
	dps := metricNamed(t, md, "hw.logical_disk.limit").Gauge().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		ids[attrs(dps.At(i))["hw.id"]] = true
	}
	assert.Equal(t, map[string]bool{"iqn.a/192.0.2.10:3260/1": true, "iqn.a/192.0.2.11:3260/1": true}, ids)
}

func TestSCSIStatesMapToHwStates(t *testing.T) {
	for state, want := range map[string]string{
		"running": "ok", "blocked": "degraded", "quiesce": "degraded", "created": "degraded", "created-blocked": "degraded",
		"offline": "failed", "transport-offline": "failed", "cancel": "failed", "deleted": "failed",
	} {
		t.Run(state, func(t *testing.T) {
			got, ok := hwState(state)
			assert.True(t, ok)
			assert.Equal(t, want, got)
		})
	}
	_, ok := hwState("unknown")
	assert.False(t, ok)
	_, ok = hwState("something new")
	assert.False(t, ok)
}

func TestAnUnknownStateHasNoStatusPointButShowsInTheInfoPoint(t *testing.T) {
	l := lun{session: 1, id: 1, device: "sdb", state: "unknown", err: errors.New("input/output error")}
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{l}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	assert.False(t, hasMetric(md, "hw.status"))
	assert.False(t, hasMetric(md, "hw.logical_disk.limit"))
	info := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 1, info.Len())
	assert.Equal(t, "unknown", attrs(info.At(0))["state"])
}

func TestAnUnreadableLUNWarnsOncePerEpisode(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	bad := lun{session: 1, id: 1, device: "sdb", state: "unknown", err: errors.New("input/output error")}
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{bad}}
	s := newLUNScraper(zap.New(core), src, 10)

	for range 3 {
		s.scrape(time.Now())
	}
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.WarnLevel).Len())

	src.lunList = []lun{okLUN(1, 1, "sdb")}
	s.scrape(time.Now())
	src.lunList = []lun{bad}
	s.scrape(time.Now())

	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.WarnLevel).Len(), "a new failure after a good read warns again")
}

func TestMoreLUNsThanTheCapAreCutAndOneInfoLineSaysSo(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	var five []lun
	for i := 1; i <= 5; i++ {
		five = append(five, okLUN(1, i, "sd"+string(rune('a'+i))))
	}
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: five}
	s := newLUNScraper(zap.New(core), src, 3)

	var md pmetric.Metrics
	for range 3 {
		md = s.scrape(time.Now())
	}

	dps := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 3, dps.Len())
	assert.Equal(t, "1", attrs(dps.At(0))["iscsi.lun"])
	assert.Equal(t, "3", attrs(dps.At(2))["iscsi.lun"], "the first LUNs in order are kept")
	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessage("More iSCSI LUNs than max_luns: the rest are not reported").Len())

	src.lunList = five[:2]
	s.scrape(time.Now())
	src.lunList = five
	s.scrape(time.Now())

	assert.Equal(t, 2, logs.FilterMessage("More iSCSI LUNs than max_luns: the rest are not reported").Len(), "the line comes back after the count fell under the cap")
}

func TestAnUnmappedLUNWhoseSessionWasNotReadIsLeftOut(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{okLUN(9, 1, "sdz"), okLUN(1, 2, "sdb")}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	dps := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 1, dps.Len())
	assert.Equal(t, "sdb", attrs(dps.At(0))["iscsi.device"])
}

func TestNoLUNsGiveNoLUNMetricsAndTheSessionMetricsStay(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	assert.True(t, hasMetric(md, "iscsi.session.state"))
	for _, name := range []string{"hw.status", "hw.logical_disk.limit", "iscsi.lun.state"} {
		assert.False(t, hasMetric(md, name), name)
	}
}

func TestAFailedLUNListingWarnsOnceAndTheSessionMetricsStay(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunErr: errors.New("permission denied")}
	s := newLUNScraper(zap.New(core), src, 10)

	var md pmetric.Metrics
	for range 3 {
		md = s.scrape(time.Now())
	}

	assert.Equal(t, 1, logs.FilterLevelExact(zapcore.WarnLevel).Len())
	assert.True(t, hasMetric(md, "iscsi.session.state"))
	assert.False(t, hasMetric(md, "iscsi.lun.state"))
}

func TestAnUnsetSizeGivesNoLimitPointAndAStatusPoint(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{
		{session: 1, id: 1, device: "sdb", state: "blocked", err: errors.New("no size")},
	}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	assert.False(t, hasMetric(md, "hw.logical_disk.limit"))
	assert.True(t, hasMetric(md, "hw.status"))
}

func TestASizeAboveTheSignedRangeIsClamped(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{
		{session: 1, id: 1, device: "sdb", state: "running", sizeBytes: ^uint64(0), hasSize: true},
	}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Now())

	assert.Equal(t, int64(1<<63-1), metricNamed(t, md, "hw.logical_disk.limit").Gauge().DataPoints().At(0).IntValue())
}

func TestTurningLUNsOffReadsNoLUNAtAll(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}, lunList: []lun{okLUN(1, 1, "sdb")}}
	cfg := testConfig()
	cfg.LUNs = false

	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), cfg, consumertest.NewNop(), src)
	md := r.scraper.scrape(time.Now())

	assert.Nil(t, r.scraper.luns)
	assert.Zero(t, src.lunReads.Load())
	assert.False(t, hasMetric(md, "iscsi.lun.state"))
	assert.True(t, hasMetric(md, "iscsi.session.state"))
}

func TestTheReceiverUsesTheLUNSourceByDefaultWithTheConfiguredCap(t *testing.T) {
	src := &fakeBoth{fakeSessions: fakeSessions{list: lunSessions()}}
	cfg := testConfig()
	cfg.MaxLUNs = 7

	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), cfg, consumertest.NewNop(), src)

	assert.NotNil(t, r.scraper.luns)
	assert.Equal(t, 7, r.scraper.maxLUNs)
}

func TestSessionsAndLUNsFromASysfsTreeMakeTheSeries(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.2004-04.com.example:nas:iscsi.lun1.abc", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")
	c.session("2", "iqn.2004-04.com.example:nas:iscsi.lun1.abc", "1", "LOGGED_IN")
	c.connection("2", "0", "192.0.2.11", "3260", "LOGGED_IN")
	c.localDisk()
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "2097152", holder: "dm-0"})
	c.lun(lunSpec{session: 2, host: 4, id: 1, state: "offline", device: "sdc", sectors: "2097152", holder: "dm-0"})
	r := newReceiver(receivertest.NewNopSettings(NewFactory().Type()), testConfig(), consumertest.NewNop(), newTestReader(c))

	md := r.scraper.scrape(time.Now())

	assert.Equal(t, int64(2), metricNamed(t, md, "iscsi.session.count").Gauge().DataPoints().At(0).IntValue())
	info := metricNamed(t, md, "iscsi.lun.state").Gauge().DataPoints()
	require.Equal(t, 2, info.Len())
	assert.Equal(t, "sdb", attrs(info.At(0))["iscsi.device"])
	assert.Equal(t, "offline", attrs(info.At(1))["state"])
	assert.Equal(t, int64(2097152*512), metricNamed(t, md, "hw.logical_disk.limit").Gauge().DataPoints().At(0).IntValue())
	failed := int64(0)
	dps := metricNamed(t, md, "hw.status").Gauge().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		if a := attrs(dps.At(i)); a["hw.state"] == "failed" && a["hw.name"] == "sdc" {
			failed = dps.At(i).IntValue()
		}
	}
	assert.Equal(t, int64(1), failed)
}
