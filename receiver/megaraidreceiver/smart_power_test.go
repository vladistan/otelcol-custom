// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func TestOnlyReadLogExtCarriesTheExtendBit(t *testing.T) {
	cmds := []smartdecode.ATACommand{
		smartdecode.IdentifyCommand(), smartdecode.SmartReadDataCommand(), smartdecode.SmartReadThresholdsCommand(),
		smartdecode.SmartReturnStatusCommand(), smartdecode.CheckPowerModeCommand(), smartdecode.DeviceStatisticsCommand(5),
	}
	for _, cmd := range cmds {
		cdb := ataCDB(cmd)
		assert.Equal(t, cmd.Is48Bit(), cdb[1]&extendBit != 0, "command %#02x", cmd.Command)
		require.NoError(t, checkPassCDB(cdb[:], 16, cmd.DataLen), "command %#02x", cmd.Command)

		flipped := cdb
		flipped[1] ^= extendBit
		assert.Error(t, checkPassCDB(flipped[:], 16, cmd.DataLen), "command %#02x with the EXTEND bit flipped", cmd.Command)
	}
	logExt := ataCDB(smartdecode.DeviceStatisticsCommand(5))
	assert.Equal(t, byte(protoPIOIn<<1|extendBit), logExt[1])
}

func powerDrive(t *testing.T, power func() (response, error)) (*fakeDevice, func(passRequest) (response, error)) {
	dev := labFake(t)
	healthy := drive(t, smarttest.HealthyATA("ZC1AAAA1"))
	fn := func(req passRequest) (response, error) {
		if req.cdb[14] == 0xE5 {
			return power()
		}
		return healthy(req)
	}
	dev.passFn = fn
	return dev, fn
}

func TestAStandbyDriveIsNotReadAndNoCommandWakesIt(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) {
		return response{status: statusSCSIError, sense: ataSense(smartdecode.ATARegisters{SectorCount: 0x00})}, nil
	})
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	res, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.False(t, stop)
	assert.Equal(t, smartStandby, res.state)
	require.Len(t, dev.passes, 1, "only CHECK POWER MODE was sent")
	assert.Equal(t, byte(0xE5), dev.passes[0].cdb[14])
}

func TestAnUnknownPowerStateLeavesTheDriveAloneAndWarnsOnce(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) { return response{status: statusOK}, nil }) // no registers returned
	s, logs, _ := newTestScraper(t, smartConfig(), dev)
	st := newCtrlState()

	res, _ := s.readOneSmart(dev, testCtrl, st, 4)
	again, _ := s.readOneSmart(dev, testCtrl, st, 4)

	assert.Equal(t, smartUnavailable, res.state)
	assert.Equal(t, smartUnavailable, again.state)
	assert.Len(t, dev.passes, 2, "one CHECK POWER MODE for each read and nothing else")
	assert.Equal(t, 1, logs.FilterMessage("The controller gave no power state for a drive, so SMART leaves it alone").Len())
}

func TestAnActiveDriveIsReadAfterThePowerCheck(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) {
		return response{status: statusSCSIError, sense: ataSense(smartdecode.ATARegisters{SectorCount: 0xFF})}, nil
	})
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	res, _ := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.Equal(t, smartOK, res.state)
	assert.Equal(t, byte(0xE5), dev.passes[0].cdb[14], "the power check goes first")
	var powerChecks int
	for _, p := range dev.passes {
		if p.cdb[14] == 0xE5 {
			powerChecks++
		}
	}
	assert.Equal(t, 1, powerChecks)
}

func TestAPowerCheckThatFailsEndsThePassForABusyController(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) { return response{}, errControllerBusy })
	s, _, _ := newTestScraper(t, smartConfig(), dev)

	res, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.True(t, stop)
	assert.Equal(t, smartUnavailable, res.state)
}

func FuzzCheckPassPacket(f *testing.F) {
	id := ataCDB(smartdecode.IdentifyCommand())
	f.Add(id[:], 16, 512)
	f.Fuzz(func(t *testing.T, cdb []byte, cdbLen, dataLen int) {
		if len(cdb) != maxCDBLen || dataLen < 0 || dataLen > maxPassData {
			return
		}
		var c [maxCDBLen]byte
		copy(c[:], cdb)
		if checkPassPacket(passPacket(c, cdbLen, dataLen)) != nil {
			return
		}
		if c[0] != scsiInquiry && c[0] != ataPassThrough16 {
			t.Fatalf("opcode %#02x passed", c[0])
		}
		if c[0] == ataPassThrough16 && c[14] == 0xEF {
			t.Fatalf("SET FEATURES passed")
		}
	})
}
