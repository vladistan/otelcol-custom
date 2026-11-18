// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

// ataSense builds the descriptor-format sense data of an ATA Status Return descriptor.
func ataSense(r smartdecode.ATARegisters) []byte {
	s := make([]byte, senseLen)
	s[0], s[7] = 0x72, 14
	s[8], s[9] = 0x09, 0x0C
	s[11], s[13], s[15], s[17], s[19], s[20], s[21] = r.Error, r.SectorCount, r.LBALow, r.LBAMid, r.LBAHigh, r.Device, r.Status
	return s
}

// drive answers pass-through frames from a smarttest fake, as a SAT bridge in the controller would.
func drive(t *testing.T, fake *smarttest.ATA) func(passRequest) (response, error) {
	t.Helper()
	return func(req passRequest) (response, error) {
		cdb := req.cdb
		if cdb[0] == scsiLogSense { // a SAS drive that refuses every page
			return response{status: statusSCSIError}, nil
		}
		require.Equal(t, byte(ataPassThrough16), cdb[0])
		cmd := smartdecode.ATACommand{
			Command: cdb[14], Features: cdb[4], SectorCount: cdb[6], LBALow: cdb[8], LBAMid: cdb[10], LBAHigh: cdb[12],
			DataLen: req.dataLen, WantRegisters: cdb[2]&flagCheckCond != 0,
		}
		if cdb[1]&^extendBit == protoPIOIn<<1 {
			cmd.Protocol = smartdecode.ProtoPIOIn
		}
		res, err := fake.ATA(cmd)
		if err != nil {
			return response{}, err
		}
		resp := response{data: res.Data}
		if res.HaveRegisters {
			resp.sense = ataSense(res.Registers)
			resp.status = statusSCSIError
		}
		return resp, nil
	}
}

func smartConfig() *Config {
	cfg := testConfig()
	cfg.Smart = true
	return cfg
}

func TestDecodingThroughTheControllerEqualsTheDirectTransport(t *testing.T) {
	direct := smarttest.HealthyATA("ZC1AAAA1")
	want, err := smartdecode.ReadATA(direct, smartdecode.ATAOptions{SkipIfStandby: true})
	require.NoError(t, err)
	dev := newFake(t)
	dev.passFn = drive(t, smarttest.HealthyATA("ZC1AAAA1"))
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	res, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	require.False(t, stop)
	require.NotNil(t, res.data)
	assert.Equal(t, smartOK, res.state)
	assert.Equal(t, want, *res.data, "the same buffers decode to the same values through both transports")
	assert.Equal(t, direct.Commands(), commandBytes(dev.passes), "the same commands in the same order")
}

func commandBytes(passes []passRequest) []uint8 {
	var out []uint8
	for _, p := range passes {
		if p.cdb[14] == 0xB0 {
			out = append(out, p.cdb[4])
		} else {
			out = append(out, p.cdb[14])
		}
	}
	return out
}

func TestADriveThatAnswersStatus12IsDroppedForTheScrape(t *testing.T) {
	dev := labFake(t)
	dev.passFn = func(passRequest) (response, error) { return response{status: statusDeviceMissing}, nil }
	s, logs, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	_, ok := find(md, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
	assert.False(t, ok, "no SMART series for a drive the controller does not know")
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "ZC1AAAA1"), "the RAID status stays")
	assert.Zero(t, logs.FilterMessage("A SMART read through the controller failed").Len())
}

func TestSmartMetricsAndStatesFromTheDecoders(t *testing.T) {
	dev := labFake(t)
	dev.passFn = drive(t, smarttest.HealthyATA("ZC1AAAA1"))
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	state, ok := find(md, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
	require.True(t, ok)
	assert.Equal(t, smartOK, state.attrs[attrSmartState])
	norm, ok := find(md, hwschema.MetricPhysicalDiskSmart, hwschema.AttrID, "ZC1AAAA1", attrAttrID, "9", attrValueType, smartAttrNorm)
	require.True(t, ok)
	assert.Equal(t, 98.0, norm.val)
	raw, _ := find(md, hwschema.MetricPhysicalDiskSmart, hwschema.AttrID, "ZC1AAAA1", attrAttrID, "9", attrValueType, smartAttrRaw)
	assert.Equal(t, 100.0, raw.val)
	hours, _ := find(md, hwschema.MetricPhysicalDiskPowerOnTime, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, 100.0*3600, hours.val)
	cycles, _ := find(md, hwschema.MetricPhysicalDiskPowerCycles, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, 12.0, cycles.val)
	_, inDefaultList := find(md, hwschema.MetricPhysicalDiskSmart, hwschema.AttrID, "ZC1AAAA1", attrAttrID, "231")
	assert.True(t, inDefaultList, "231 is in the default list")
	sas, _ := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	assert.Equal(t, smartUnsupported, sas.attrs[attrSmartState], "a SAS drive that refuses the log pages")
}

func TestAPredictedFailureAnswerRaisesTheDriveState(t *testing.T) {
	failing := smarttest.HealthyATA("ZC1AAAA1")
	failing.Status = smarttest.ATAStatus{LBAMid: 0xF4, LBAHigh: 0x2C}
	dev := labFake(t)
	dev.passFn = drive(t, failing)
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StatePredictedFailure, current(t, md, typePhysicalDisk, "ZC1AAAA1"))
	state, _ := find(md, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, smartPredicted, state.attrs[attrSmartState])
}

func TestSmartOffUnsupportedAndStandbyAreReportedAsSuch(t *testing.T) {
	tests := map[string]struct {
		mutate func(*smarttest.ATA)
		want   string
	}{
		"disabled": {func(a *smarttest.ATA) {
			a.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "m", Serial: "s", SMARTSupported: true, SMARTEnabled: false})
		}, smartOff},
		"unsupported": {func(a *smarttest.ATA) {
			a.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{Model: "m", Serial: "s"})
		}, smartUnsupported},
		"standby": {func(a *smarttest.ATA) { a.PowerMode = 0 }, smartStandby},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := smarttest.HealthyATA("ZC1AAAA1")
			tt.mutate(fake)
			dev := labFake(t)
			dev.passFn = drive(t, fake)
			s, _, _ := newTestScraper(t, smartConfig(), dev)

			md := s.scrape(context.Background()).metrics

			state, ok := find(md, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
			require.True(t, ok)
			assert.Equal(t, tt.want, state.attrs[attrSmartState])
			for _, p := range dev.passes {
				if p.cdb[14] == 0xB0 {
					assert.Contains(t, []byte{0xD0, 0xD1, 0xDA}, p.cdb[4], "no SMART control command is ever sent")
				}
			}
			_, series := find(md, hwschema.MetricPhysicalDiskSmart, hwschema.AttrID, "ZC1AAAA1")
			assert.False(t, series)
		})
	}
}

func TestEachDriveIsReadOncePerIntervalInSerialOrder(t *testing.T) {
	dev := labFake(t)
	healthy := map[uint8]*smarttest.ATA{4: smarttest.HealthyATA("ZC1AAAA1"), 5: smarttest.HealthyATA("ZC1AAAA2")}
	dev.passFn = func(req passRequest) (response, error) { return drive(t, healthy[req.target])(req) }
	cfg := smartConfig()
	s, _, clock := newTestScraper(t, cfg, dev)

	first := s.scrape(context.Background())
	n := len(dev.passes)
	require.NotZero(t, n)
	var order []uint8
	for _, p := range dev.passes {
		if len(order) == 0 || order[len(order)-1] != p.target {
			order = append(order, p.target)
		}
	}
	assert.Equal(t, []uint8{4, 5, 6}, order, "one drive at a time, in list order, the SAS drive last")

	clock.advance(5 * time.Minute)
	second := s.scrape(context.Background())
	assert.Len(t, dev.passes, n, "no query before the interval ends")
	_, kept := find(second.metrics, hwschema.MetricPhysicalDiskSmart, hwschema.AttrID, "ZC1AAAA1")
	assert.True(t, kept, "the last result is sent again")
	assert.Equal(t, len(points(first.metrics, hwschema.MetricPhysicalDiskSmart)), len(points(second.metrics, hwschema.MetricPhysicalDiskSmart)))

	clock.advance(10 * time.Minute)
	s.scrape(context.Background())
	assert.Len(t, dev.passes, 2*n, "every drive is read again after 15 minutes")
}

func TestRebuildingFailedAndPatrolledDrivesAreNotQueried(t *testing.T) {
	dev := labFake(t)
	dev.setMbox(opPDGetInfo, mailbox(4, 0), pdInfoBuf(pdSpec{id: 4, vendor: "ATA", serial: "A", fw: pdRebuild, rebuildPct: 100}))
	dev.setMbox(opPDGetInfo, mailbox(5, 0), pdInfoBuf(pdSpec{id: 5, vendor: "ATA", serial: "B", fw: pdOnline, patrolPct: 100}))
	dev.setMbox(opPDGetInfo, mailbox(6, 0), pdInfoBuf(pdSpec{id: 6, vendor: "ATA", serial: "C", fw: pdFailed}))
	dev.passFn = drive(t, smarttest.HealthyATA("x"))
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	s.scrape(context.Background())

	assert.Empty(t, dev.passes)
}

func TestSmartIsOffByDefault(t *testing.T) {
	assert.False(t, createDefaultConfig().(*Config).Smart)
	dev := labFake(t)
	dev.passFn = drive(t, smarttest.HealthyATA("x"))
	s, _, _ := newTestScraper(t, testConfig(), dev)

	s.scrape(context.Background())

	assert.Empty(t, dev.passes)
}

func TestATransportErrorWarnsOnceAndAControllerErrorStopsTheDrives(t *testing.T) {
	dev := labFake(t)
	dev.passFn = func(passRequest) (response, error) { return response{}, errControllerBusy }
	s, logs, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	assert.Len(t, dev.passes, 1, "the first busy answer ends the SMART reads of the scrape")
	assert.Equal(t, 1, logs.FilterMessage("A SMART read through the controller failed").Len())
	state, _ := find(md, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
	assert.Equal(t, smartUnavailable, state.attrs[attrSmartState])
}

func TestAStatusOtherThanSuccessIsAnError(t *testing.T) {
	dev := newFake(t)
	dev.passFn = func(passRequest) (response, error) { return response{status: 0x33}, nil }
	tr := passTransport{dev: dev, target: 4}

	_, err := tr.ATA(smartdecode.IdentifyCommand())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 51")
	_, err = tr.ATA(smartdecode.ATACommand{Command: 0xB0, Features: 0xD8, LBAMid: 0x4F, LBAHigh: 0xC2})
	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, "SMART ENABLE never leaves the transport")
	assert.Len(t, dev.passes, 1, "only the IDENTIFY reached the device")
}

func TestADriveIdAbove255CannotBeAddressed(t *testing.T) {
	dev := labFake(t)
	dev.set(opPDGetList, pdListBuf(pdAddr{DeviceID: 300}))
	dev.setMbox(opPDGetInfo, mailbox(0x2C, 0x01), pdInfoBuf(pdSpec{id: 300, vendor: "ATA", serial: "BIG", fw: pdOnline}))
	dev.passFn = drive(t, smarttest.HealthyATA("x"))
	s, logs, _ := newTestScraper(t, smartConfig(), dev)

	s.scrape(context.Background())

	assert.Empty(t, dev.passes)
	assert.Equal(t, 1, logs.FilterMessage("A drive id is above 255 and cannot be addressed for SMART").Len())
}

func TestPassTransportReturnsSenseRegisters(t *testing.T) {
	dev := newFake(t)
	want := smartdecode.ATARegisters{LBAMid: 0x4F, LBAHigh: 0xC2, Status: 0x50}
	dev.passFn = func(passRequest) (response, error) {
		return response{status: statusSCSIError, sense: ataSense(want)}, nil
	}

	res, err := passTransport{dev: dev, target: 1}.ATA(smartdecode.SmartReturnStatusCommand())

	require.NoError(t, err)
	assert.True(t, res.HaveRegisters)
	assert.Equal(t, want, res.Registers)
	_, err = passTransport{dev: &errPass{}, target: 1}.ATA(smartdecode.IdentifyCommand())
	assert.Error(t, err)
}

type errPass struct{}

func (errPass) exec(request) (response, error)     { return response{}, errors.New("x") }
func (errPass) pass(passRequest) (response, error) { return response{}, errors.New("pass failed") }
