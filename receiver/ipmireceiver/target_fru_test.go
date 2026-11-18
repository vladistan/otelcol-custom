// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap/zapcore"
)

const (
	fruInfo   = "inventory.bmc.fru.info"
	fruStatus = "inventory.collection.status"
)

func attrsOf(dp pmetric.NumberDataPoint) map[string]string {
	out := map[string]string{}
	dp.Attributes().Range(func(k string, v pcommon.Value) bool { out[k] = v.AsString(); return true })
	return out
}

func fruSeries(md pmetric.Metrics) map[string]map[string]string {
	out := map[string]map[string]string{}
	dps := pmetric.NewNumberDataPointSlice()
	if md.ResourceMetrics().Len() > 0 {
		dps = dataPoints(md, fruInfo)
	}
	for i := 0; i < dps.Len(); i++ {
		a := attrsOf(dps.At(i))
		out[a["fru.type"]+"/"+a["hw.id"]] = a
	}
	return out
}

func statusValues(md pmetric.Metrics) []int64 {
	var out []int64
	dps := dataPoints(md, fruStatus)
	for i := 0; i < dps.Len(); i++ {
		out = append(out, dps.At(i).IntValue())
	}
	return out
}

// fruBoard is a lab board with the built-in FRU (chassis, board, product) and one DIMM.
func fruBoard(clock *fakeClock) *fakeBMC {
	b := labBoard(clock, 1, 0, 0)
	b.frus = map[uint8][]byte{
		0: fruImage(
			fruChassisArea(0x17, fruText8("CH-PART"), fruText8("CH-SER")),
			fruBoardArea(fruText8("BoardMaker"), fruText8("X9DRW"), fruText8("BD-SER"), fruText8("BD-PART")),
			fruProductArea(fruText8("ProdMaker"), fruText8("SuperServer"), fruText8("PR-PART"), fruText8("v1"), fruText8("PR-SER"), fruText8("ASSET")),
		),
		5: fruImage(nil, fruBoardArea(fruText8("DimmMaker"), fruText8("M393A2K43"), fruText8("DIMM-SER"), fruText8("DIMM-PART")), nil),
	}
	b.fruDevices = []fruDevice{{id: 5, name: "DIMM A1", kind: "dimm"}, {id: 6, name: "CPU1", kind: "cpu"}}
	return b
}

func TestFRUGivesOneSeriesPerRecordWithItsType(t *testing.T) {
	clock := newClock()
	r := newRig(t, fruBoard(clock), clock, nil)

	md := r.scrape(t)

	got := fruSeries(md)
	require.Len(t, got, 4, "board, product, chassis and the DIMM; the CPU has no FRU")
	assert.Equal(t, map[string]string{"fru.type": "board", "hw.id": "fru/0", "hw.vendor": "BoardMaker", "hw.model": "X9DRW", "hw.serial_number": "BD-SER", "hw.part_number": "BD-PART"}, got["board/fru/0"])
	assert.Equal(t, "ProdMaker", got["product/fru/0"]["hw.vendor"])
	assert.Equal(t, "ASSET", got["product/fru/0"]["hw.asset_tag"])
	assert.Equal(t, "Rack Mount Chassis", got["chassis/fru/0"]["chassis.type"])
	assert.Equal(t, map[string]string{"fru.type": "dimm", "hw.id": "fru/5", "hw.name": "DIMM A1", "hw.vendor": "DimmMaker", "hw.model": "M393A2K43", "hw.serial_number": "DIMM-SER", "hw.part_number": "DIMM-PART"}, got["dimm/fru/5"])
	dps := dataPoints(md, fruInfo)
	for i := 0; i < dps.Len(); i++ {
		assert.Equal(t, int64(1), dps.At(i).IntValue())
	}
	assert.Equal(t, []int64{1}, statusValues(md))
	st := dataPoints(md, fruStatus).At(0)
	assert.Equal(t, map[string]string{"inventory.source": "fru"}, attrsOf(st))
	up, _ := upValue(t, md)
	assert.Equal(t, int64(1), up)
}

func TestFRUIntervalDefaultsTo24HoursAndTheReadRunsOncePerInterval(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	r := newRig(t, board, clock, nil)
	assert.Equal(t, 24*time.Hour, r.target.cfg.FRUInterval)

	first := r.scrape(t)
	reads := board.fruReadCount
	clock.Advance(time.Hour)
	second := r.scrape(t)
	clock.Advance(23*time.Hour + time.Minute)
	third := r.scrape(t)

	assert.NotEmpty(t, fruSeries(first))
	assert.Empty(t, fruSeries(second))
	assert.Equal(t, reads, board.fruReadCount-reads, "the third scrape reads again, the second did not")
	assert.NotEmpty(t, fruSeries(third))
	assert.Equal(t, 2, board.fruListCount)
}

func TestFRUNotExposedGivesNoSeriesAndNoError(t *testing.T) {
	clock := newClock()
	board := labBoard(clock, 1, 0, 0)
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	assert.Empty(t, fruSeries(md))
	assert.Equal(t, []int64{1}, statusValues(md))
	assert.Empty(t, r.logs.FilterLevelExact(zapcore.WarnLevel).All())
}

func TestFRUErrorNeverFailsTheScrapeAndRetriesAfterAnHour(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruErrs = map[uint8]error{0: errors.New("completion code 0xff")}
	r := newRig(t, board, clock, nil)

	first := r.scrape(t)

	up, _ := upValue(t, first)
	assert.Equal(t, int64(1), up)
	assert.Equal(t, []int64{0}, statusValues(first))
	assert.Equal(t, 1, pointCount(first, "hw.temperature"))

	clock.Advance(30 * time.Minute)
	assert.Empty(t, fruSeries(r.scrape(t)))
	assert.Equal(t, 1, board.fruListCount, "no retry inside the hour")

	clock.Advance(31 * time.Minute)
	board.fruErrs = nil
	retry := r.scrape(t)
	assert.NotEmpty(t, fruSeries(retry))
	assert.Equal(t, []int64{1}, statusValues(retry))
}

func TestFRUFailuresLogOneWarningPerEpisode(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruErrs = map[uint8]error{0: errors.New("boom")}
	r := newRig(t, board, clock, nil)

	r.scrape(t)
	clock.Advance(2 * time.Hour)
	r.scrape(t)

	warns := r.logs.FilterLevelExact(zapcore.WarnLevel).All()
	assert.Len(t, warns, 1)
	for _, w := range r.logs.All() {
		assert.NotContains(t, w.Message, "BD-SER")
		for _, f := range w.Context {
			assert.NotContains(t, f.String, "BD-SER")
		}
	}

	board.fruErrs = nil
	clock.Advance(2 * time.Hour)
	r.scrape(t)
	board.fruErrs = map[uint8]error{0: errors.New("boom")}
	clock.Advance(25 * time.Hour)
	r.scrape(t)
	assert.Len(t, r.logs.FilterLevelExact(zapcore.WarnLevel).All(), 2, "a new episode warns again")
}

func TestFRUCorruptImageGivesStatusZeroAndNoPanic(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	bad := append([]byte{}, board.frus[0]...)
	bad[7]++
	board.frus[0] = bad
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	got := fruSeries(md)
	assert.Len(t, got, 1, "the DIMM is still read")
	assert.Equal(t, []int64{0}, statusValues(md))
}

func TestFRUDeviceListErrorKeepsTheBuiltInRecords(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruListErr = errors.New("sdr failed")
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	assert.Len(t, fruSeries(md), 3)
	assert.Equal(t, []int64{0}, statusValues(md))
}

func TestFRUPassReadsAtMostTheDeviceCapOf200ListedDevices(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruDevices = nil
	for i := 1; i <= 200; i++ {
		id := uint8(i)
		board.fruDevices = append(board.fruDevices, fruDevice{id: id, name: fmt.Sprintf("DIMM %d", i), kind: "dimm"})
		board.frus[id] = board.frus[5]
	}
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	assert.LessOrEqual(t, board.fruReadCount, fruMaxDevices+1)
	assert.Len(t, fruSeries(md), 3+fruMaxDevices)
}

func TestFRUIsNotReadWhenTheScrapeFails(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.dialErr = errTimeout
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	assert.Zero(t, board.fruReadCount)
	assert.ElementsMatch(t, []string{"hw.bmc.up", "hw.bmc.scrape.duration"}, metricNamesOf(md))
}

func TestFRUDeviceThatCannotBeReadIsNotAnErrorAndIsReportedOnce(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruDevices = []fruDevice{
		{id: 5, name: "DIMM A1", kind: "dimm"},
		{id: 7, name: "DIMM A2", kind: "dimm", isSPD: true},
		{id: 8, name: "DIMM A3", kind: "dimm", isRemote: true},
		{id: 9, name: "DIMM A4", kind: "dimm"},
	}
	board.frus[7] = []byte{0x92, 0x11, 0x0b, 0x02, 0, 0, 0, 0} // JEDEC SPD bytes, not an IPMI FRU image
	board.fruErrs = map[uint8]error{9: errFRUUnsupported}
	r := newRig(t, board, clock, nil)

	first := r.scrape(t)
	clock.Advance(25 * time.Hour)
	r.scrape(t)

	assert.Equal(t, []int64{1}, statusValues(first), "devices of another format are not a failed source")
	assert.Contains(t, fruSeries(first), "dimm/fru/5")
	assert.Len(t, fruSeries(first), 4)
	assert.Empty(t, r.logs.FilterLevelExact(zapcore.WarnLevel).All())
	noted := r.logs.FilterMessageSnippet("not read")
	require.Equal(t, 1, noted.Len(), "one line for both reads")
	assert.Equal(t, int64(3), noted.All()[0].ContextMap()["devices"])
	assert.NotContains(t, board.fruReadIDs(), uint8(8), "a device behind another controller is never asked")
}

func TestFRUDeviceWithADamagedImageIsStillAnError(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruDevices = []fruDevice{{id: 5, name: "DIMM A1", kind: "dimm"}}
	board.frus[5] = []byte{0x92, 0x11, 0x0b, 0x02, 0, 0, 0, 0}
	board.fruErrs = map[uint8]error{5: errFRUCorrupt}
	r := newRig(t, board, clock, nil)

	assert.Equal(t, []int64{0}, statusValues(r.scrape(t)))
}

func TestFRUPassDoesNotCountInTheScrapeDurationAndDoesNotStarveThePowerRead(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	board.fruLatency = 120 * time.Millisecond
	r := newRig(t, board, clock, func(c *BMCConfig) { c.DCMIPower = true })
	r.target.env.scrapeTimeout = 80 * time.Millisecond

	md := r.scrape(t)

	dur := dataPoints(md, "hw.bmc.scrape.duration").At(0).DoubleValue()
	assert.Less(t, dur, 0.05, "the FRU time is not part of the scrape")
	require.Len(t, board.powerCtxErrs, 1)
	assert.NoError(t, board.powerCtxErrs[0], "the power read keeps the time the sensors left")
	assert.Equal(t, 1, pointCount(md, "hw.host.power"))
	assert.NotEmpty(t, fruSeries(md))
}

func TestSessionIsClosedWithALiveContextEvenWhenTheParentEndsDuringTheFRUPass(t *testing.T) {
	clock := newClock()
	board := fruBoard(clock)
	ctx, cancel := context.WithCancel(t.Context())
	board.fruHook = cancel
	r := newRig(t, board, clock, nil)

	r.target.scrape(ctx)

	require.Equal(t, 1, board.closed)
	require.Len(t, board.closeCtxErrs, 1)
	assert.NoError(t, board.closeCtxErrs[0], "Close Session is sent with a fresh context")
}
