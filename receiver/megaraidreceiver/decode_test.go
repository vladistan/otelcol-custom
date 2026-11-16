// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

func TestDecodeCtrlInfo(t *testing.T) {
	raw := ctrlBuf(ctrlOpts{
		device: pciDevice3108, product: "LSI MegaRAID SAS 9361-8i", serial: "SV12345678", fw: "4.680.00-8527", memMB: 1024,
		maxLDs: 64, bbu: true, ldOffline: 1, ldDegraded: 2, pdPresent: 8, pdPredFail: 1, pdFailed: 3, roc: 70, board: 41,
	})

	c, err := decodeCtrlInfo(raw)

	require.NoError(t, err)
	assert.Equal(t, "LSI MegaRAID SAS 9361-8i", c.Product)
	assert.Equal(t, "SV12345678", c.Serial)
	assert.Equal(t, "4.680.00-8527", c.Firmware)
	assert.Equal(t, 1024, c.MemoryMB)
	assert.True(t, c.HasBBU)
	assert.True(t, c.supported())
	assert.Equal(t, 1, c.LDOffline)
	assert.Equal(t, 2, c.LDDegraded)
	assert.Equal(t, 8, c.PDPresent)
	assert.Equal(t, 1, c.PDPredFail)
	assert.Equal(t, 3, c.PDFailed)
	assert.Equal(t, 70, c.TempROC)
	assert.Equal(t, 41, c.TempBoard)
}

func TestControllerWithoutTemperatureFieldsGivesNone(t *testing.T) {
	c, err := decodeCtrlInfo(ctrlBuf(ctrlOpts{device: pciDevice2208, product: "x"}))

	require.NoError(t, err)
	assert.Zero(t, c.TempROC)
	assert.Zero(t, c.TempBoard)
	short := ctrlBuf(ctrlOpts{device: pciDevice2208})[:ctrlMinLen]
	c, err = decodeCtrlInfo(short)
	require.NoError(t, err)
	assert.Zero(t, c.TempROC)
}

func TestListCommandsFollowTheControllerLimits(t *testing.T) {
	assert.False(t, ctrlInfo{MaxLDs: 64, PDPresent: 240}.extendedLDs())
	assert.True(t, ctrlInfo{MaxLDs: 240}.extendedLDs())
	assert.False(t, ctrlInfo{PDPresent: 240}.extendedPDs())
	assert.True(t, ctrlInfo{PDPresent: 241}.extendedPDs())
}

func TestSupportedListHoldsTheTwoGenerations(t *testing.T) {
	assert.True(t, ctrlInfo{PCIVendor: pciVendorBroadcom, PCIDevice: pciDevice2208}.supported())
	assert.True(t, ctrlInfo{PCIVendor: pciVendorBroadcom, PCIDevice: pciDevice3108}.supported())
	assert.False(t, ctrlInfo{PCIVendor: pciVendorBroadcom, PCIDevice: 0x0016}.supported())
	assert.False(t, ctrlInfo{PCIVendor: 0x8086, PCIDevice: pciDevice2208}.supported())
}

func TestDecodeLists(t *testing.T) {
	pd, err := decodePDList(pdListBuf(pdAddr{DeviceID: 4, EnclID: 32, Slot: 1}, pdAddr{DeviceID: 4}, pdAddr{DeviceID: 9, SCSIType: 0x0d}))
	require.NoError(t, err)
	require.Len(t, pd, 2, "a repeated id is read once")
	assert.Equal(t, pdAddr{DeviceID: 4, EnclID: 32, Slot: 1}, pd[0])
	assert.Equal(t, uint8(0x0d), pd[1].SCSIType)

	ld, err := decodeLDList(ldListBuf(0, 1, 1, 5))
	require.NoError(t, err)
	assert.Equal(t, []uint8{0, 1, 5}, ld)

	q, err := decodeLDQuery(ldQueryBuf(2, 3))
	require.NoError(t, err)
	assert.Equal(t, []uint8{2, 3}, q)
}

func TestDecodeLDInfoAndMappings(t *testing.T) {
	spec := ldSpec{target: 2, name: "vol", level: 1, spans: 2, stripeLog2: 7, drives: 4, state: ldStateDegraded, cache: 0x05, blocks: 1000, checkPct: 32768, reconPct: 65535}

	l, err := decodeLDInfo(ldInfoBuf(spec))

	require.NoError(t, err)
	assert.Equal(t, "vol", l.Name)
	assert.Equal(t, uint64(1000*512), l.SizeBytes)
	assert.Equal(t, "raid10", raidLevel(l.Level, l.SpanDepth))
	assert.Equal(t, uint64(65536), stripBytes(l.StripeLog2))
	assert.InDelta(t, 0.5, l.Progress[opCheck], 0.001)
	assert.InDelta(t, 1.0, l.Progress[opReconstruction], 0.001)
	assert.NotContains(t, l.Progress, opBackgroundInit)
	name, cache, err := decodeLDProps(ldPropBuf(spec))
	require.NoError(t, err)
	assert.Equal(t, "vol", name)
	assert.Equal(t, uint8(5), cache)
}

func TestRaidLevelNames(t *testing.T) {
	tests := []struct {
		primary uint8
		spans   int
		want    string
	}{{0, 1, "raid0"}, {1, 1, "raid1"}, {1, 2, "raid10"}, {5, 1, "raid5"}, {5, 2, "raid50"}, {6, 1, "raid6"}, {6, 3, "raid60"}, {9, 1, "unknown"}}
	for _, tt := range tests {
		assert.Equal(t, tt.want, raidLevel(tt.primary, tt.spans))
	}
	assert.Zero(t, stripBytes(stripeLog2Max+1))
}

func TestLogicalDriveStateMapping(t *testing.T) {
	tests := map[uint8]string{ldStateOptimal: hwschema.StateOK, ldStatePartial: hwschema.StateDegraded, ldStateDegraded: hwschema.StateDegraded, ldStateOffline: hwschema.StateFailed}
	for in, want := range tests {
		got, ok := ldState(in)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	_, ok := ldState(9)
	assert.False(t, ok)
}

func TestPhysicalDriveStateMapping(t *testing.T) {
	tests := []struct {
		fw   uint16
		pred uint32
		want string
	}{
		{pdOnline, 0, hwschema.StateOK}, {pdHotSpare, 0, hwschema.StateOK}, {pdUnconfiguredGood, 0, hwschema.StateOK}, {pdSystem, 0, hwschema.StateOK},
		{pdRebuild, 0, hwschema.StateDegraded}, {pdCopyback, 0, hwschema.StateDegraded},
		{pdFailed, 0, hwschema.StateFailed}, {pdOffline, 0, hwschema.StateFailed}, {pdUnconfiguredBad, 0, hwschema.StateFailed},
		{pdOnline, 3, hwschema.StatePredictedFailure}, {pdFailed, 3, hwschema.StateFailed},
	}
	for _, tt := range tests {
		got, ok := pdState(tt.fw, tt.pred)
		assert.True(t, ok)
		assert.Equal(t, tt.want, got, "state %#x predicted failures %d", tt.fw, tt.pred)
	}
	_, ok := pdState(0x77, 0)
	assert.False(t, ok)
}

func TestControllerStateFromCounts(t *testing.T) {
	assert.Equal(t, hwschema.StateOK, controllerState(ctrlInfo{}))
	assert.Equal(t, hwschema.StateDegraded, controllerState(ctrlInfo{LDDegraded: 1}))
	assert.Equal(t, hwschema.StateDegraded, controllerState(ctrlInfo{PDPredFail: 1}))
	assert.Equal(t, hwschema.StateFailed, controllerState(ctrlInfo{LDOffline: 1, LDDegraded: 1}))
	assert.Equal(t, hwschema.StateFailed, controllerState(ctrlInfo{PDFailed: 2}))
}

func TestDecodePDInfo(t *testing.T) {
	raw := pdInfoBuf(pdSpec{id: 7, vendor: "ATA", product: "ST4000NM0033", serial: "ZC1AAAA1", fw: pdRebuild, media: 4, other: 5, pred: 6, blocks: 2048, encl: 32, slot: 3, temp: 33, rebuildPct: 16384, patrolPct: 65535})

	p, err := decodePDInfo(raw)

	require.NoError(t, err)
	assert.Equal(t, "ATA", p.Vendor)
	assert.Equal(t, "ST4000NM0033", p.Product)
	assert.Equal(t, "ZC1AAAA1", p.Serial)
	assert.Equal(t, uint16(32), p.EnclID)
	assert.Equal(t, uint8(3), p.Slot)
	assert.Equal(t, uint64(2048*512), p.SizeBytes)
	assert.Equal(t, 33, p.Temperature)
	assert.Equal(t, uint32(4), p.MediaErrors)
	assert.InDelta(t, 0.25, p.Progress[opRebuild], 0.001)
	assert.InDelta(t, 1.0, p.Progress[opPatrol], 0.001)
}

func TestDecodeBattery(t *testing.T) {
	status, capacity, props := bbuBuffers(bbuSpec{status: bbuVoltageLow, voltage: 3900, temp: 36, pct: 80, cycles: 12, runMin: 90, nextLearn: 100})

	b, err := decodeBBU(status, capacity, props)

	require.NoError(t, err)
	assert.Equal(t, 3900, b.VoltageMV)
	assert.Equal(t, 36, b.Temperature)
	assert.Equal(t, 80, b.ChargePct)
	assert.Equal(t, 12, b.CycleCount)
	assert.True(t, b.hasNextLearn)
	assert.Equal(t, hwschema.StateDegraded, bbuState(b.Status))
	assert.Equal(t, hwschema.StateFailed, bbuState(bbuPackMissing))
	assert.Equal(t, hwschema.StateFailed, bbuState(bbuI2CError))
	assert.Equal(t, hwschema.StateOK, bbuState(0x08), "charging alone is healthy")
	_, err = decodeBBU(status[:4], nil, nil)
	assert.ErrorIs(t, err, errShort)
	b, err = decodeBBU(status, nil, nil)
	require.NoError(t, err)
	assert.False(t, b.hasCapacity)
}

func TestDecodeEvents(t *testing.T) {
	list := evtListBuf(event{Sequence: 7, Code: 99, Locale: 8, Class: classWarning, Text: "PD removed"}, event{Sequence: 8, Class: classDebug, Text: "dbg"})

	events, err := decodeEvents(list)

	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, event{Sequence: 7, Code: 99, Locale: 8, Class: classWarning, Text: "PD removed"}, events[0])
	assert.Equal(t, classDebug, events[1].Class)
	st, err := decodeEventState(evtStateBuf(40, 3))
	require.NoError(t, err)
	assert.Equal(t, eventState{Newest: 40, Oldest: 3}, st)
}

func TestClassNamesRoundTrip(t *testing.T) {
	for c := classDebug; c <= classDead; c++ {
		got, ok := parseClass(className(c))
		assert.True(t, ok)
		assert.Equal(t, c, got)
	}
	_, ok := parseClass("loud")
	assert.False(t, ok)
	assert.Equal(t, "unknown", className(42))
}

func TestShortBuffersGiveErrorsNotPanics(t *testing.T) {
	_, err := decodeCtrlInfo(make([]byte, 10))
	assert.ErrorIs(t, err, errShort)
	_, err = decodePDList(nil)
	assert.ErrorIs(t, err, errShort)
	_, err = decodeLDList(nil)
	assert.ErrorIs(t, err, errShort)
	_, err = decodeLDQuery(nil)
	assert.ErrorIs(t, err, errShort)
	_, err = decodeLDInfo(make([]byte, 5))
	assert.ErrorIs(t, err, errShort)
	_, _, err = decodeLDProps(nil)
	assert.ErrorIs(t, err, errShort)
	_, err = decodePDInfo(make([]byte, 5))
	assert.ErrorIs(t, err, errShort)
	_, err = decodeEvents(nil)
	assert.ErrorIs(t, err, errShort)
	_, err = decodeEventState(nil)
	assert.ErrorIs(t, err, errShort)
}

// FuzzDecoders feeds every decoder buffers of any length and content: none may panic or read past the end.
func FuzzDecoders(f *testing.F) {
	f.Add(ctrlBuf(ctrlOpts{device: pciDevice2208, product: "p"}))
	f.Add(pdListBuf(pdAddr{DeviceID: 1}))
	f.Add(ldInfoBuf(ldSpec{target: 1, blocks: 9}))
	f.Add(pdInfoBuf(pdSpec{id: 1, vendor: "ATA"}))
	f.Add(evtListBuf(event{Sequence: 1, Text: "x"}))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = decodeCtrlInfo(raw)
		_, _ = decodePDList(raw)
		_, _ = decodeLDList(raw)
		_, _ = decodeLDQuery(raw)
		_, _ = decodeLDInfo(raw)
		_, _, _ = decodeLDProps(raw)
		_, _ = decodePDInfo(raw)
		_, _ = decodeBBU(raw, raw, raw)
		_, _ = decodeEvents(raw)
		_, _ = decodeEventState(raw)
	})
}

func TestEveryTruncationOfARealBufferIsSafe(t *testing.T) {
	buffers := [][]byte{ctrlBuf(ctrlOpts{device: pciDevice2208, product: "p", bbu: true, roc: 50}), pdListBuf(pdAddr{DeviceID: 1}, pdAddr{DeviceID: 2}), ldInfoBuf(ldSpec{target: 1, blocks: 9, checkPct: 5}), pdInfoBuf(pdSpec{id: 1, vendor: "ATA", rebuildPct: 3}), evtListBuf(event{Sequence: 1, Text: "x"})}
	for _, b := range buffers {
		for n := 0; n <= len(b); n += 7 {
			_, _ = decodeCtrlInfo(b[:n])
			_, _ = decodePDList(b[:n])
			_, _ = decodeLDInfo(b[:n])
			_, _ = decodePDInfo(b[:n])
			_, _ = decodeEvents(b[:n])
		}
		long := append(append([]byte(nil), b...), make([]byte, 999)...)
		_, _ = decodeCtrlInfo(long)
		_, _ = decodePDInfo(long)
	}
}
