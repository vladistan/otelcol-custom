// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

// errAny marks a table row that expects some error without naming it.
var errAny = errors.New("any error")

// logSenseAnswer answers LOG SENSE frames from a fake SAS drive and fails the test on any other CDB.
func logSenseAnswer(t *testing.T, fake *smarttest.SAS) func(passRequest) (response, error) {
	t.Helper()
	return func(req passRequest) (response, error) {
		require.Equal(t, byte(scsiLogSense), req.cdb[0], "only LOG SENSE goes to a SAS drive")
		require.NoError(t, checkPassCDB(req.cdb[:], req.cdbLen, req.dataLen))
		page, err := fake.SCSI(smartdecode.SCSICommand{Page: req.cdb[2] & pageCodeMask, DataLen: req.dataLen})
		if errors.Is(err, smartdecode.ErrUnsupported) {
			return response{status: statusSCSIError}, nil
		}
		return response{data: page}, err
	}
}

func TestLogSenseCDBOfEveryListedPagePassesTheCheck(t *testing.T) {
	for _, page := range []uint8{0x00, 0x02, 0x03, 0x05, 0x0D, 0x0E, 0x2F} {
		cdb := logSenseCDB(smartdecode.LogSenseCommand(page))

		assert.NoError(t, checkPassCDB(cdb[:], 10, smartdecode.LogDataLen), "page %#x", page)
		assert.Equal(t, byte(0x4D), cdb[0])
		assert.Equal(t, byte(0x40)|page, cdb[2], "cumulative values of the page")
		assert.Equal(t, []byte{0x02, 0x00}, cdb[7:9], "allocation length 512, big endian")
	}
}

func TestLogSenseOfAnyOtherPageIsRefused(t *testing.T) {
	listed := map[uint8]bool{0x00: true, 0x02: true, 0x03: true, 0x05: true, 0x0D: true, 0x0E: true, 0x2F: true}
	for page := 0; page < 64; page++ {
		cdb := logSenseCDB(smartdecode.LogSenseCommand(uint8(page)))

		err := checkPassCDB(cdb[:], 10, smartdecode.LogDataLen)

		assert.Equal(t, !listed[uint8(page)], err != nil, "page %#x", page)
	}
}

func TestALogSenseWithAnyOtherFieldIsRefused(t *testing.T) {
	good := logSenseCDB(smartdecode.LogSenseCommand(0x03))
	cases := map[string]func(c *[maxCDBLen]byte, n *int, data *int){
		"PPC bit":          func(c *[maxCDBLen]byte, _, _ *int) { c[1] = 0x02 },
		"save parameters":  func(c *[maxCDBLen]byte, _, _ *int) { c[1] = 0x01 },
		"threshold values": func(c *[maxCDBLen]byte, _, _ *int) { c[2] = 0x03 },
		"default values":   func(c *[maxCDBLen]byte, _, _ *int) { c[2] = 0xC3 },
		"subpage":          func(c *[maxCDBLen]byte, _, _ *int) { c[3] = 1 },
		"parameter ptr":    func(c *[maxCDBLen]byte, _, _ *int) { c[5] = 1 },
		"control byte":     func(c *[maxCDBLen]byte, _, _ *int) { c[9] = 1 },
		"trailing bytes":   func(c *[maxCDBLen]byte, _, _ *int) { c[12] = 1 },
		"short cdb":        func(_ *[maxCDBLen]byte, n *int, _ *int) { *n = 6 },
		"length mismatch":  func(_ *[maxCDBLen]byte, _ *int, data *int) { *data = 256 },
	}
	for name, mutate := range cases {
		cdb, n, data := good, 10, smartdecode.LogDataLen
		mutate(&cdb, &n, &data)

		assert.Error(t, checkPassCDB(cdb[:], n, data), name)
	}
}

func TestOtherReadCommandsStayRefused(t *testing.T) {
	for _, op := range []byte{0x28, 0x2A, 0x2F, 0x4C, 0x55, 0x3B, 0x1B, 0x04, 0x35} {
		var cdb [maxCDBLen]byte
		cdb[0] = op

		assert.Error(t, checkPassCDB(cdb[:], 10, 512), "opcode %#x", op)
	}
}

func TestPassTransportMapsTheControllerStatusesOfALogSense(t *testing.T) {
	for name, tt := range map[string]struct {
		status  uint8
		data    []byte
		wantErr error // nil: success; errAny: any error
	}{
		"ok":            {status: statusOK, data: []byte{1, 2}},
		"refused page":  {status: statusSCSIError, wantErr: smartdecode.ErrUnsupported},
		"drive missing": {status: statusDeviceMissing, wantErr: errDriveGone},
		"other failure": {status: 9, wantErr: errAny},
	} {
		t.Run(name, func(t *testing.T) {
			dev := newFake(t)
			dev.passFn = func(passRequest) (response, error) { return response{status: tt.status, data: tt.data}, nil }

			got, err := passTransport{dev: dev, target: 6}.SCSI(smartdecode.LogSenseCommand(0x0D))

			if tt.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, tt.data, got)
				return
			}
			require.Error(t, err)
			if !errors.Is(tt.wantErr, errAny) {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestPassTransportRefusesACommandOffTheListBeforeTheFrame(t *testing.T) {
	dev := newFake(t)
	tr := passTransport{dev: dev, target: 6}

	_, err := tr.SCSI(smartdecode.SCSICommand{Page: 0x38, DataLen: smartdecode.LogDataLen})

	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed)
	assert.Empty(t, dev.passes)
}

// sasDevice is the lab controller whose LOG SENSE frames go to the fake SAS drive and the rest to an ATA drive.
func sasDevice(t *testing.T, fake *smarttest.SAS) *fakeDevice {
	t.Helper()
	dev := labFake(t)
	dev.passFn = func(req passRequest) (response, error) {
		if req.cdb[0] == scsiLogSense {
			return logSenseAnswer(t, fake)(req)
		}
		return drive(t, smarttest.HealthyATA("ZC1AAAA1"))(req)
	}
	return dev
}

func sasScraper(t *testing.T, fake *smarttest.SAS) (*scraper, *fakeDevice) {
	t.Helper()
	dev := sasDevice(t, fake)
	s, _, _ := newTestScraper(t, smartConfig(), dev)
	return s, dev
}

func TestASasDriveReportsHealthTemperatureErrorsAndCycles(t *testing.T) {
	s, dev := sasScraper(t, smarttest.HealthySAS())

	md := s.scrape(context.Background()).metrics

	state, ok := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	require.True(t, ok)
	assert.Equal(t, smartOK, state.attrs[attrSmartState], "no longer not_ata")
	cycles, found := find(md, hwschema.MetricPhysicalDiskPowerCycles, hwschema.AttrID, "S0M1BBB3")
	require.True(t, found)
	assert.Equal(t, 250.0, cycles.val)
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "S0M1BBB3"))
	var sasCmds int
	for _, p := range dev.passes {
		if p.cdb[0] == scsiLogSense {
			sasCmds++
		}
	}
	assert.Equal(t, 7, sasCmds, "the page list and six pages")
}

func TestASasDriveWithNoTemperatureOfItsOwnTakesItFromTheLogPage(t *testing.T) {
	fake := smarttest.HealthySAS()
	fake.Pages[0x0D] = smarttest.LogPage(0x0D, smarttest.LogParam(0x0000, 0x00, 44))
	dev := sasDevice(t, fake)
	dev.setMbox(opPDGetInfo, mailbox(6, 0), pdInfoBuf(pdSpec{
		id: 6, vendor: "SEAGATE", product: "ST300MM0006", serial: "S0M1BBB3", fw: pdOnline, blocks: 1 << 20, encl: 32, slot: 2,
	}))
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	temp, found := find(md, hwschema.MetricTemperature, hwschema.AttrID, "S0M1BBB3")
	require.True(t, found)
	assert.Equal(t, 44.0, temp.val, "the temperature page wins over the exceptions page (38)")
}

func TestASasDriveWithAnExceptionIsAPredictedFailure(t *testing.T) {
	fake := smarttest.HealthySAS()
	fake.Pages[0x2F] = smarttest.LogPage(0x2F, smarttest.LogParam(0x0000, 0x5D, 0x10, 38))
	s, _ := sasScraper(t, fake)

	md := s.scrape(context.Background()).metrics

	state, _ := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	assert.Equal(t, smartPredicted, state.attrs[attrSmartState])
	assert.Equal(t, hwschema.StatePredictedFailure, current(t, md, typePhysicalDisk, "S0M1BBB3"))
}

func TestARefusedSasPageIsSkippedCountedAndWarnedOnce(t *testing.T) {
	fake := smarttest.HealthySAS()
	delete(fake.Pages, 0x03)
	s, logs, _ := newTestScraper(t, smartConfig(), sasDevice(t, fake))

	md := s.scrape(context.Background()).metrics
	s.scrape(context.Background())

	state, _ := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	assert.Equal(t, smartOK, state.attrs[attrSmartState])
	_, readFound := find(md, hwschema.MetricErrors, hwschema.AttrID, "S0M1BBB3", hwschema.AttrErrorType, errorUncorrected, attrDirection, "read")
	assert.False(t, readFound, "no read error count without its page")
	_, writeFound := find(md, hwschema.MetricErrors, hwschema.AttrID, "S0M1BBB3", hwschema.AttrErrorType, errorUncorrected, attrDirection, "write")
	assert.True(t, writeFound, "the other pages are read")
	assert.Equal(t, 1, logs.FilterMessage("A SAS drive refused or garbled log pages").Len())
}

func TestASasDriveThatRefusesTheWholePageListIsUnsupported(t *testing.T) {
	fake := &smarttest.SAS{Pages: map[uint8][]byte{}}
	s, _ := sasScraper(t, fake)

	md := s.scrape(context.Background()).metrics

	state, ok := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	require.True(t, ok)
	assert.Equal(t, smartUnsupported, state.attrs[attrSmartState])
}

func TestASasDriveIsReadOncePerSmartInterval(t *testing.T) {
	s, dev := sasScraper(t, smarttest.HealthySAS())

	s.scrape(context.Background())
	first := len(dev.passes)
	s.scrape(context.Background())

	assert.Equal(t, first, len(dev.passes), "the second scrape is inside smart_interval")
}

func TestASasDriveTheControllerDoesNotKnowIsDropped(t *testing.T) {
	dev := labFake(t)
	dev.passFn = func(req passRequest) (response, error) {
		if req.cdb[0] == scsiLogSense {
			return response{status: statusDeviceMissing}, nil
		}
		return drive(t, smarttest.HealthyATA("ZC1AAAA1"))(req)
	}
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	_, ok := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	assert.False(t, ok)
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "S0M1BBB3"), "the RAID status stays")
}

func TestDocsStateThatSasHealthIsReadAndTheSampleKeepsSmartOn(t *testing.T) {
	section := megaraidSection(t)

	for _, want := range []string{"**SAS drives.**", "LOG SENSE", "4Dh", "`unsupported`", "Direct SAS disks"} {
		assert.Contains(t, section, want)
	}
	assert.NotContains(t, section, "not_ata")
	assert.Contains(t, readDoc(t, "test/configs/edge-megaraid.yaml"), "smart: true")
	assert.Contains(t, readDoc(t, "docs/smart.md"), "megaraid")
}

func TestWithSmartOffNoLogSenseIsSent(t *testing.T) {
	dev := labFake(t)
	dev.passFn = func(passRequest) (response, error) {
		t.Fatal("no pass-through frame with smart off")
		return response{}, nil
	}
	s, _, _ := newTestScraper(t, testConfig(), dev)

	s.scrape(context.Background())

	assert.Empty(t, dev.passes)
}

func fixedSense(key, asc uint8) []byte {
	s := make([]byte, senseLen)
	s[0], s[2], s[12] = 0x70, key, asc
	return s
}

func TestPassTransportReadsTheSenseKeyOfACheckCondition(t *testing.T) {
	for name, tt := range map[string]struct {
		sense     []byte
		transient bool
		refused   bool
	}{
		"illegal request": {fixedSense(0x05, 0x24), false, true},
		"unit attention":  {fixedSense(0x06, 0x29), true, false},
		"not ready":       {fixedSense(0x02, 0x04), true, false},
		"medium error":    {fixedSense(0x03, 0x11), false, false},
		"no sense":        {nil, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			dev := newFake(t)
			dev.passFn = func(passRequest) (response, error) {
				return response{status: statusSCSIError, sense: tt.sense}, nil
			}

			_, err := passTransport{dev: dev, target: 6}.SCSI(smartdecode.LogSenseCommand(0x0D))

			require.Error(t, err)
			assert.Equal(t, tt.transient, errors.Is(err, smartdecode.ErrTransient))
			assert.Equal(t, tt.refused, errors.Is(err, smartdecode.ErrUnsupported))
		})
	}
}

// uaOnce answers the first n LOG SENSE frames with UNIT ATTENTION, then like the fake drive.
func uaOnce(t *testing.T, fake *smarttest.SAS, n int) func(passRequest) (response, error) {
	t.Helper()
	answer := logSenseAnswer(t, fake)
	return func(req passRequest) (response, error) {
		if req.cdb[0] != scsiLogSense {
			return drive(t, smarttest.HealthyATA("ZC1AAAA1"))(req)
		}
		if n > 0 {
			n--
			return response{status: statusSCSIError, sense: fixedSense(0x06, 0x29)}, nil
		}
		return answer(req)
	}
}

func TestAUnitAttentionOnThePageListIsRetriedWithinTheRead(t *testing.T) {
	dev := labFake(t)
	dev.passFn = uaOnce(t, smarttest.HealthySAS(), 1)
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics

	state, ok := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	require.True(t, ok)
	assert.Equal(t, smartOK, state.attrs[attrSmartState])
}

func TestATransientDriveIsUnavailableAndReadAgainOnTheNextScrape(t *testing.T) {
	dev := labFake(t)
	calls := 1 << 30
	dev.passFn = uaOnce(t, smarttest.HealthySAS(), calls)
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	md := s.scrape(context.Background()).metrics
	first := len(dev.passes)
	s.scrape(context.Background())

	state, ok := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	require.True(t, ok)
	assert.Equal(t, smartUnavailable, state.attrs[attrSmartState], "not unsupported")
	assert.Greater(t, len(dev.passes), first, "no full smart_interval wait after a transient answer")
}

func TestATemperatureWarningOnTheExceptionsPageIsNotAPredictedFailure(t *testing.T) {
	fake := smarttest.HealthySAS()
	fake.Pages[0x2F] = smarttest.LogPage(0x2F, smarttest.LogParam(0x0000, 0x0B, 0x01, 70))
	s, _ := sasScraper(t, fake)

	md := s.scrape(context.Background()).metrics

	state, _ := find(md, metricSmartState, hwschema.AttrID, "S0M1BBB3")
	assert.Equal(t, smartOK, state.attrs[attrSmartState])
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "S0M1BBB3"))
}

func TestSasUncorrectedErrorsUseTheirOwnTypeWithADirection(t *testing.T) {
	s, _ := sasScraper(t, smarttest.HealthySAS())

	md := s.scrape(context.Background()).metrics

	for dir, want := range map[string]float64{"read": 2, "write": 0, "verify": 5} {
		e, ok := find(md, hwschema.MetricErrors, hwschema.AttrID, "S0M1BBB3",
			hwschema.AttrErrorType, errorUncorrected, attrDirection, dir)
		require.True(t, ok, dir)
		assert.Equal(t, want, e.val, dir)
	}
	for _, old := range []string{"read", "write", "verify"} {
		_, ok := find(md, hwschema.MetricErrors, hwschema.AttrID, "S0M1BBB3", hwschema.AttrErrorType, old)
		assert.False(t, ok, "error.type %s is gone", old)
	}
}

func TestDocsNameTheSasErrorSeriesAndTheStandbyLimit(t *testing.T) {
	section := megaraidSection(t)

	for _, want := range []string{"`uncorrected`", "`raid.error.direction`", "standby", "skip_if_standby"} {
		assert.Contains(t, section, want)
	}
	assert.Contains(t, readDoc(t, "docs/smart.md"), "SAS")
}
