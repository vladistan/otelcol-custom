// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

type pt struct {
	attrs map[string]string
	val   float64
}

// points returns the data points of every metric with a name.
func points(md pmetric.Metrics, name string) []pt {
	var out []pt
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		sms := md.ResourceMetrics().At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				if m.Name() != name {
					continue
				}
				var dps pmetric.NumberDataPointSlice
				if m.Type() == pmetric.MetricTypeSum {
					dps = m.Sum().DataPoints()
				} else {
					dps = m.Gauge().DataPoints()
				}
				for d := 0; d < dps.Len(); d++ {
					dp := dps.At(d)
					p := pt{attrs: map[string]string{}}
					dp.Attributes().Range(func(k string, v pcommon.Value) bool { p.attrs[k] = v.AsString(); return true })
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						p.val = float64(dp.IntValue())
					} else {
						p.val = dp.DoubleValue()
					}
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// current returns the hw.state of the point with value 1 of hw.status for a component.
func current(t *testing.T, md pmetric.Metrics, typ, id string) string {
	t.Helper()
	var got []string
	n := 0
	for _, p := range points(md, metricStatus) {
		if p.attrs[hwschema.AttrType] != typ || p.attrs[hwschema.AttrID] != id {
			continue
		}
		n++
		if p.val == 1 {
			got = append(got, p.attrs[hwschema.AttrState])
		}
	}
	require.Equal(t, len(states), n, "one point for each state of %s", id)
	require.Len(t, got, 1, "exactly one current state of %s", id)
	return got[0]
}

func find(md pmetric.Metrics, name string, kv ...string) (pt, bool) {
	for _, p := range points(md, name) {
		match := true
		for i := 0; i+1 < len(kv); i += 2 {
			if p.attrs[kv[i]] != kv[i+1] {
				match = false
			}
		}
		if match {
			return p, true
		}
	}
	return pt{}, false
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var testCtrl = controller{HostNo: 0, PCI: "0000:03:00.0"}

func testConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Events.Enabled = false
	return cfg
}

func newTestScraper(t *testing.T, cfg *Config, dev device, ctrlList ...controller) (*scraper, *observer.ObservedLogs, *testClock) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	clock := &testClock{t: time.Unix(1_700_000_000, 0)}
	if len(ctrlList) == 0 {
		ctrlList = []controller{testCtrl}
	}
	s := newScraper(zap.New(core), cfg,
		func() ([]controller, error) { return ctrlList, nil },
		func(int) (device, func(), error) { return dev, func() {}, nil },
		clock.now)
	return s, logs, clock
}

func TestScrapeEmitsTheLabHost(t *testing.T) {
	s, logs, _ := newTestScraper(t, testConfig(), labFake(t))

	out := s.scrape(context.Background())

	md := out.metrics
	p, ok := find(md, metricCtrlInfo, "hw.id", "0000:03:00.0")
	require.True(t, ok)
	assert.Equal(t, "PERC H710P Mini", p.attrs[hwschema.AttrModel])
	assert.Equal(t, "3.130.05-1234", p.attrs[hwschema.AttrFirmwareVersion])
	assert.Equal(t, "CONTROLLER0001", p.attrs[hwschema.AttrSerialNumber])
	assert.Equal(t, "1024", p.attrs[attrMemory])
	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:03:00.0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeLogicalDisk, "0000:03:00.0/ld0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "ZC1AAAA1"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeBattery, "0000:03:00.0/bbu"))
	assert.Zero(t, logs.FilterMessage("This MegaRAID product is outside the supported list and is read best-effort").Len(), "a 2208 is supported")

	reach, _ := find(md, metricReachable)
	assert.Equal(t, 1.0, reach.val)
	roc, _ := find(md, metricTemperature, attrLocation, sensorROC)
	assert.Equal(t, 61.0, roc.val)
	board, _ := find(md, metricTemperature, attrLocation, sensorBoard)
	assert.Equal(t, 44.0, board.val)

	level, _ := find(md, metricLDLevel)
	assert.Equal(t, "raid5", level.attrs[attrLevel])
	assert.Equal(t, "data", level.attrs[hwschema.AttrName])
	strip, _ := find(md, metricLDStrip)
	assert.Equal(t, 65536.0, strip.val)
	limit, _ := find(md, metricLDLimit)
	assert.Equal(t, float64(1<<21)*512, limit.val)
	cache, _ := find(md, metricLDCache)
	assert.Equal(t, "0x03", cache.attrs[attrCache])

	size, _ := find(md, metricPDSize, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, float64(1<<22)*512, size.val)
	temp, _ := find(md, metricTemperature, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, 34.0, temp.val)
	media, _ := find(md, metricErrors, hwschema.AttrID, "ZC1AAAA1", hwschema.AttrErrorType, hwschema.ErrorMedia)
	assert.Equal(t, 2.0, media.val)
	pdStatus, _ := find(md, metricStatus, hwschema.AttrID, "S0M1BBB3")
	assert.Equal(t, "0000:03:00.0", pdStatus.attrs[hwschema.AttrParent])
	assert.Equal(t, "32", pdStatus.attrs[attrEnclosure])
	assert.Equal(t, "2", pdStatus.attrs[attrSlot])
	assert.Equal(t, "ATA ST4000NM0033", func() string {
		p, _ := find(md, metricStatus, hwschema.AttrID, "ZC1AAAA1")
		return p.attrs[hwschema.AttrName]
	}())

	charge, _ := find(md, metricBatteryCharge)
	assert.InDelta(t, 0.97, charge.val, 0.0001)
	cycles, _ := find(md, metricBatteryCycles)
	assert.Equal(t, 41.0, cycles.val)
	left, _ := find(md, metricBatteryTimeLeft)
	assert.Equal(t, 36000.0, left.val)
	learn, _ := find(md, metricBatteryLearn)
	assert.Equal(t, float64(600000000+mfiEpoch), learn.val)
	bt, _ := find(md, metricTemperature, attrLocation, sensorBattery)
	assert.Equal(t, 30.0, bt.val)

	_, enclosure := find(md, metricStatus, hwschema.AttrID, "32")
	assert.False(t, enclosure, "the enclosure device is not a drive")
}

func TestScrapeSendsNothingElseThanAllowedReads(t *testing.T) {
	dev := labFake(t) // the fake fails the test on any opcode outside the list
	s, _, _ := newTestScraper(t, testConfig(), dev)

	s.scrape(context.Background())

	assert.NotEmpty(t, dev.sent)
	assert.Empty(t, dev.passes, "no drive command without smart")
	assert.Zero(t, dev.count(opEventGet)+dev.count(opEventGetInfo), "no event read with events off")
}

func TestStatesFollowTheCountsAndTheFirmwareStates(t *testing.T) {
	dev := labFake(t)
	dev.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{device: pciDevice2208, product: "p", maxLDs: 64, ldDegraded: 1, pdPresent: 3}))
	ld := ldSpec{target: 0, level: 5, spans: 1, state: ldStateOffline}
	dev.setMbox(opLDGetInfo, mailbox(0), ldInfoBuf(ld))
	dev.setMbox(opPDGetInfo, mailbox(4, 0), pdInfoBuf(pdSpec{id: 4, vendor: "ATA", serial: "S4", fw: pdFailed}))
	dev.setMbox(opPDGetInfo, mailbox(5, 0), pdInfoBuf(pdSpec{id: 5, vendor: "ATA", serial: "S5", fw: pdRebuild, rebuildPct: 32768}))
	dev.setMbox(opPDGetInfo, mailbox(6, 0), pdInfoBuf(pdSpec{id: 6, vendor: "ATA", serial: "S6", fw: pdOnline, pred: 2}))
	s, _, _ := newTestScraper(t, testConfig(), dev)

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StateDegraded, current(t, md, typeController, "0000:03:00.0"))
	assert.Equal(t, hwschema.StateFailed, current(t, md, typeLogicalDisk, "0000:03:00.0/ld0"))
	assert.Equal(t, hwschema.StateFailed, current(t, md, typePhysicalDisk, "S4"))
	assert.Equal(t, hwschema.StateDegraded, current(t, md, typePhysicalDisk, "S5"))
	assert.Equal(t, hwschema.StatePredictedFailure, current(t, md, typePhysicalDisk, "S6"))
	prog, ok := find(md, metricProgress, hwschema.AttrID, "S5", attrOperation, opRebuild)
	require.True(t, ok)
	assert.InDelta(t, 0.5, prog.val, 0.001)
}

func TestLogicalDriveProgressAndUnknownStates(t *testing.T) {
	dev := labFake(t)
	spec := ldSpec{target: 0, level: 5, spans: 1, state: 9, checkPct: 65535, bgiPct: 16384}
	dev.setMbox(opLDGetInfo, mailbox(0), ldInfoBuf(spec))
	dev.setMbox(opPDGetInfo, mailbox(4, 0), pdInfoBuf(pdSpec{id: 4, vendor: "ATA", serial: "S4", fw: 0x77}))
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	md := s.scrape(context.Background()).metrics
	s.scrape(context.Background())

	_, hasLD := find(md, metricStatus, hwschema.AttrID, "0000:03:00.0/ld0")
	assert.False(t, hasLD, "an unknown state sends no status")
	check, _ := find(md, metricProgress, attrOperation, opCheck)
	assert.InDelta(t, 1.0, check.val, 0.001)
	bgInit, _ := find(md, metricProgress, attrOperation, opBackgroundInit)
	assert.InDelta(t, 0.25, bgInit.val, 0.001)
	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.WarnLevel).Len(), "one warning for each unknown value across two scrapes")
}

func TestExtendedListsAreChosenFromTheControllerLimits(t *testing.T) {
	tests := map[string]struct {
		maxLDs     uint8
		pdPresent  uint16
		wantLDList uint32
		wantPDList uint32
	}{
		"plain":    {64, 3, opLDGetList, opPDGetList},
		"extended": {240, 300, opLDListQuery, opPDListQuery},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dev := newFake(t)
			dev.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{device: pciDevice3108, product: "p", maxLDs: tt.maxLDs, pdPresent: tt.pdPresent}))
			dev.set(opLDGetList, ldListBuf(0))
			dev.set(opLDListQuery, ldQueryBuf(0))
			dev.set(opPDGetList, pdListBuf(pdAddr{DeviceID: 4}))
			dev.set(opPDListQuery, pdListBuf(pdAddr{DeviceID: 4}))
			dev.setMbox(opLDGetInfo, mailbox(0), ldInfoBuf(ldSpec{target: 0, level: 0, state: ldStateOptimal}))
			dev.setMbox(opPDGetInfo, mailbox(4, 0), pdInfoBuf(pdSpec{id: 4, vendor: "ATA", serial: "S4", fw: pdOnline}))
			s, _, _ := newTestScraper(t, testConfig(), dev)

			s.scrape(context.Background())

			other := map[uint32]uint32{opLDGetList: opLDListQuery, opLDListQuery: opLDGetList, opPDGetList: opPDListQuery, opPDListQuery: opPDGetList}
			for _, want := range []uint32{tt.wantLDList, tt.wantPDList} {
				assert.Equal(t, 1, dev.count(want), allowed[want])
				assert.Zero(t, dev.count(other[want]), allowed[other[want]])
			}
		})
	}
}

func TestAListCommandTheControllerRejectsFallsBackToTheOther(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opLDGetList, statusInvalidDCMD)
	dev.set(opLDListQuery, ldQueryBuf(0))
	s, _, _ := newTestScraper(t, testConfig(), dev)

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StateOK, current(t, md, typeLogicalDisk, "0000:03:00.0/ld0"))
}

func TestARejectedCommandIsSentOncePerController(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opBBUGetCapacityInfo, statusInvalidDCMD)
	other := controller{HostNo: 1, PCI: "0000:04:00.0"}
	s, logs, _ := newTestScraper(t, testConfig(), dev, testCtrl, other)

	s.scrape(context.Background())
	s.scrape(context.Background())

	assert.Equal(t, 2, dev.count(opBBUGetCapacityInfo), "once for each of the two controllers, never again")
	assert.Equal(t, 2, logs.FilterMessage("The controller does not know a command and it is not sent again until the receiver restarts").Len())
}

func TestANotSupportedCommandIsAskedAtTheNextScrape(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opBBUGetCapacityInfo, statusNotSupported)
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	for range 3 {
		s.scrape(context.Background())
	}

	assert.Equal(t, 3, dev.count(opBBUGetCapacityInfo), "a not supported answer is no reason to stop asking")
	assert.Equal(t, 1, logs.FilterMessage("The controller reports a command as not supported and it is asked again at every scrape").Len(), "one line for the episode")
	assert.Zero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestAnInvalidCommandIsRememberedUntilRestart(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opBBUGetCapacityInfo, statusInvalidDCMD)
	s, _, clock := newTestScraper(t, testConfig(), dev)

	s.scrape(context.Background())
	clock.advance(30 * 24 * time.Hour)
	s.scrape(context.Background())
	s.scrape(context.Background())

	assert.Equal(t, 1, dev.count(opBBUGetCapacityInfo), "never again, however long the receiver runs")
}

func TestACommandThatWorksAgainIsUsedAgain(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opBBUGetCapacityInfo, statusNotSupported)
	s, logs, _ := newTestScraper(t, testConfig(), dev)
	s.scrape(context.Background())
	dev.setStatus(opBBUGetCapacityInfo, statusOK)

	s.scrape(context.Background())
	s.scrape(context.Background())
	dev.setStatus(opBBUGetCapacityInfo, statusNotSupported)
	s.scrape(context.Background())

	assert.Equal(t, 4, dev.count(opBBUGetCapacityInfo), "read at every scrape")
	assert.Equal(t, 2, logs.FilterMessage("The controller reports a command as not supported and it is asked again at every scrape").Len(), "a new episode is logged again")
}

func TestOtherStatusesWarnOncePerCommand(t *testing.T) {
	dev := labFake(t)
	dev.setStatus(opBBUGetProp, 0x33)
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	for range 3 {
		s.scrape(context.Background())
	}

	assert.Equal(t, 1, logs.FilterMessage("The controller refused a read command").Len())
	assert.Equal(t, 3, dev.count(opBBUGetProp), "a refusal is not a reason to stop asking")
}

func TestAControllerWithoutABatteryGetsNoBatteryCommand(t *testing.T) {
	dev := labFake(t)
	dev.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{device: pciDevice2208, product: "no bbu", maxLDs: 64, bbu: false, pdPresent: 3}))
	s, _, _ := newTestScraper(t, testConfig(), dev)

	md := s.scrape(context.Background()).metrics

	for _, op := range []uint32{opBBUGetStatus, opBBUGetCapacityInfo, opBBUGetDesignInfo, opBBUGetProp} {
		assert.Zero(t, dev.count(op), allowed[op])
	}
	_, ok := find(md, metricStatus, hwschema.AttrType, typeBattery)
	assert.False(t, ok)
}

func TestAProductOutsideTheListLogsOneInfoLine(t *testing.T) {
	const msg = "This MegaRAID product is outside the supported list and is read best-effort"
	dev := labFake(t)
	dev.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{device: 0x0016, product: "SAS3508", maxLDs: 64, pdPresent: 3}))
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	s.scrape(context.Background())
	s.scrape(context.Background())

	got := logs.FilterMessage(msg)
	require.Equal(t, 1, got.Len())
	assert.Equal(t, "SAS3508", got.All()[0].ContextMap()["product"])
	assert.Equal(t, zapcore.InfoLevel, got.All()[0].Level)
}

func TestSkipClassesLogOncePerEpisode(t *testing.T) {
	tests := map[string]error{
		"capability": errNeedsCapability,
		"node":       errNodeMissing,
		"gone":       errControllerGone,
		"busy":       errControllerBusy,
		"timeout":    errTimeout,
		"broken":     errDeviceBroken,
		"interrupt":  errInterrupted,
		"rejected":   errRejected,
	}
	for name, failure := range tests {
		t.Run(name, func(t *testing.T) {
			dev := labFake(t)
			dev.fail[opCtrlGetInfo] = failure
			s, logs, _ := newTestScraper(t, testConfig(), dev)

			first := s.scrape(context.Background())
			second := s.scrape(context.Background())

			assert.Equal(t, 1, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())
			_, anyStatus := find(first.metrics, metricStatus)
			assert.False(t, anyStatus, "a skipped scrape sends no status")
			_, anySecond := find(second.metrics, metricReachable)
			assert.False(t, anySecond)
			assert.Equal(t, 2, dev.count(opCtrlGetInfo), "the controller is asked again at the next scrape")
		})
	}
}

func TestOpenErrorsSkipTheScrapeToo(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	s := newScraper(zap.New(core), testConfig(),
		func() ([]controller, error) { return []controller{testCtrl}, nil },
		func(int) (device, func(), error) { return nil, nil, errNeedsCapability },
		time.Now)

	for range 2 {
		out := s.scrape(context.Background())
		assert.Zero(t, out.metrics.MetricCount())
	}

	assert.Equal(t, 1, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())
}

func TestENODEVSendsReachableZeroOnceAndRecovers(t *testing.T) {
	dev := labFake(t)
	dev.fail[opCtrlGetInfo] = errControllerGone
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	first := s.scrape(context.Background())
	second := s.scrape(context.Background())

	r, ok := find(first.metrics, metricReachable)
	require.True(t, ok)
	assert.Equal(t, 0.0, r.val)
	_, ok = find(second.metrics, metricReachable)
	assert.False(t, ok, "zero is sent once")
	assert.Equal(t, 1, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())

	delete(dev.fail, opCtrlGetInfo)
	third := s.scrape(context.Background())
	r, ok = find(third.metrics, metricReachable)
	require.True(t, ok)
	assert.Equal(t, 1.0, r.val)

	dev.fail[opCtrlGetInfo] = errControllerGone
	fourth := s.scrape(context.Background())
	_, ok = find(fourth.metrics, metricReachable)
	assert.True(t, ok, "a new episode sends zero again")
	assert.Equal(t, 2, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())
}

func TestAnUnexpectedErrorAndAShortAnswerSkipTheController(t *testing.T) {
	dev := labFake(t)
	dev.set(opCtrlGetInfo, make([]byte, 20))
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	out := s.scrape(context.Background())

	assert.Zero(t, out.metrics.MetricCount())
	assert.Equal(t, 1, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())

	dev2 := labFake(t)
	dev2.setStatus(opCtrlGetInfo, 0x33)
	s2, logs2, _ := newTestScraper(t, testConfig(), dev2)
	assert.Zero(t, s2.scrape(context.Background()).metrics.MetricCount())
	assert.Equal(t, 1, logs2.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())
}

func TestNoControllerLogsOneInfoLineAndSendsNothing(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	opened := 0
	s := newScraper(zap.New(core), testConfig(),
		func() ([]controller, error) { return nil, nil },
		func(int) (device, func(), error) { opened++; return nil, nil, errors.New("must not open") },
		time.Now)

	for range 3 {
		out := s.scrape(context.Background())
		assert.Zero(t, out.metrics.MetricCount())
	}

	assert.Zero(t, opened)
	assert.Equal(t, 1, logs.FilterMessage("No megaraid_sas controller found; nothing is sent to any device").Len())
}

func TestListingFailureWarnsOnce(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	s := newScraper(zap.New(core), testConfig(),
		func() ([]controller, error) { return nil, errors.New("sysfs unreadable") },
		func(int) (device, func(), error) { return nil, nil, errors.New("no") }, time.Now)

	s.scrape(context.Background())
	s.scrape(context.Background())

	assert.Equal(t, 1, logs.FilterMessage("Cannot list the megaraid_sas controllers").Len())
}

func TestScrapeCoversEveryControllerInTurn(t *testing.T) {
	devs := map[int]device{0: labFake(t), 1: labFake(t)}
	core, _ := observer.New(zapcore.DebugLevel)
	ctrlList := []controller{{HostNo: 0, PCI: "0000:03:00.0"}, {HostNo: 1, PCI: "0000:82:00.0"}}
	s := newScraper(zap.New(core), testConfig(),
		func() ([]controller, error) { return ctrlList, nil },
		func(h int) (device, func(), error) { return devs[h], func() {}, nil }, time.Now)

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:03:00.0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:82:00.0"))
	assert.Equal(t, 2, md.ResourceMetrics().Len())
}

func TestADriveWithoutASerialIsNamedByControllerAndDevice(t *testing.T) {
	dev := labFake(t)
	dev.setMbox(opPDGetInfo, mailbox(4, 0), pdInfoBuf(pdSpec{id: 4, vendor: "ATA", fw: pdOnline}))
	s, _, _ := newTestScraper(t, testConfig(), dev)

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "0000:03:00.0/pd4"))
}

func TestControllerWithoutPCIAddressUsesTheHostNumber(t *testing.T) {
	assert.Equal(t, "megaraid-host5", controller{HostNo: 5}.id())
}

func TestOnlyOneScrapeRunsAtATime(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	dev := labFake(t)
	dev.dynamic[opCtrlGetInfo] = func(request) (response, error) {
		entered <- struct{}{}
		<-release
		return response{data: ctrlBuf(ctrlOpts{device: pciDevice2208, product: "p", maxLDs: 64})}, nil
	}
	s, _, _ := newTestScraper(t, testConfig(), dev)
	done := make(chan output, 1)
	go func() { done <- s.scrape(context.Background()) }()
	<-entered

	second := s.scrape(context.Background())

	assert.Zero(t, second.metrics.MetricCount(), "no second scrape starts")
	assert.Equal(t, 1, dev.count(opCtrlGetInfo))
	close(release)
	assert.NotZero(t, (<-done).metrics.MetricCount())
}

func TestFindControllersReadsSysfs(t *testing.T) {
	root := t.TempDir()
	mk := func(host, driver string) {
		dir := filepath.Join(root, "devices", "pci0000:00", "0000:00:03.0", "0000:03:00.0", host+"dev", "scsi_host", host)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "proc_name"), []byte(driver+"\n"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "class", "scsi_host"), 0o755))
		require.NoError(t, os.Symlink(dir, filepath.Join(root, "class", "scsi_host", host)))
	}
	mk("host0", "megaraid_sas")
	mk("host1", "ata_piix")
	mk("host12", "megaraid_sas")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "class", "scsi_host", "not-a-host"), 0o755))

	got, err := findControllers(root)

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 0, got[0].HostNo)
	assert.Equal(t, 12, got[1].HostNo)
	assert.Equal(t, "0000:03:00.0", got[0].PCI)
}

func TestFindControllersWithoutSysfsFindsNone(t *testing.T) {
	got, err := findControllers(t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFindControllersReportsAnUnreadableDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "class"), []byte("x"), 0o644))

	_, err := findControllers(root)

	assert.Error(t, err)
}

func TestAShortLogicalDriveWarnsOncePerController(t *testing.T) {
	dev := labFake(t)
	dev.setMbox(opLDGetProp, mailbox(0), []byte{1, 2, 3})
	s, logs, _ := newTestScraper(t, testConfig(), dev)

	out := s.scrape(context.Background())
	s.scrape(context.Background())

	assert.Equal(t, 1, logs.FilterMessage("A logical drive properties answer is shorter than expected").Len(), "one line, not one for each scrape")
	level, ok := find(out.metrics, metricLDLevel)
	require.True(t, ok, "the drive is still reported from its info answer")
	assert.Equal(t, "raid5", level.attrs[attrLevel])
}
