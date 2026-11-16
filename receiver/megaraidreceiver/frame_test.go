// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func okPacket(op uint32) []byte {
	return buildPacket(3, request{opcode: op, dataLen: 2048}, 0x1000)
}

func TestIoctlNumberAndPacketSizeAreGolden(t *testing.T) {
	// _IOWR('M', 1, 404 bytes): direction 3, size 404, type 'M', number 1.
	want := uintptr(3)<<30 | uintptr(packetSize)<<16 | uintptr('M')<<8 | 1

	assert.Equal(t, uintptr(0xC1944D01), ioctlFirmware)
	assert.Equal(t, want, ioctlFirmware)
	assert.Equal(t, 404, packetSize)
	assert.Equal(t, 20+128+16*16, packetSize)
}

func TestDCMDPacketOffsetsAreGolden(t *testing.T) {
	var mb [mboxSize]byte
	mb[0], mb[1] = 0xAB, 0xCD
	p := buildPacket(7, request{opcode: opPDGetInfo, mbox: mb, dataLen: 512}, 0xDEAD0000)

	le := binary.LittleEndian
	assert.Len(t, p, 404)
	assert.Equal(t, uint16(7), le.Uint16(p[0:]), "host_no")
	assert.Equal(t, uint32(40), le.Uint32(p[4:]), "sgl_off")
	assert.Equal(t, uint32(1), le.Uint32(p[8:]), "sge_count")
	assert.Zero(t, le.Uint32(p[12:]), "sense_off")
	assert.Zero(t, le.Uint32(p[16:]), "sense_len")
	f := p[20:148]
	assert.Equal(t, byte(5), f[0], "MFI_CMD_DCMD")
	assert.Equal(t, byte(1), f[7], "frame sge_count")
	assert.Equal(t, uint16(0x10), le.Uint16(f[16:]), "flags read")
	assert.Equal(t, uint32(512), le.Uint32(f[20:]), "data_xfer_len")
	assert.Equal(t, opPDGetInfo, le.Uint32(f[24:]), "opcode")
	assert.Equal(t, []byte{0xAB, 0xCD}, f[28:30], "mailbox")
	assert.Equal(t, uint64(0xDEAD0000), le.Uint64(p[148:]), "buffer address")
	assert.Equal(t, uint64(512), le.Uint64(p[156:]), "buffer length")
	require.NoError(t, checkPacket(p))
}

func TestCheckPacketRefusesEveryOddField(t *testing.T) {
	tests := map[string]func(p []byte){
		"short":           nil,
		"padding":         func(p []byte) { p[2] = 1 },
		"sgl_off":         func(p []byte) { p[4] = 41 },
		"sge count":       func(p []byte) { p[8] = 2 },
		"sense off":       func(p []byte) { p[12] = 8 },
		"sense len":       func(p []byte) { p[16] = 8 },
		"not a DCMD":      func(p []byte) { p[20] = 4 },
		"frame sense len": func(p []byte) { p[21] = 1 },
		"status set":      func(p []byte) { p[22] = 1 },
		"target set":      func(p []byte) { p[24] = 1 },
		"frame sge":       func(p []byte) { p[27] = 2 },
		"context":         func(p []byte) { p[28] = 1 },
		"flags":           func(p []byte) { p[36] = 0x30 },
		"timeout":         func(p []byte) { p[38] = 1 },
		"zero length":     func(p []byte) { binary.LittleEndian.PutUint32(p[40:], 0) },
		"huge length":     func(p []byte) { binary.LittleEndian.PutUint32(p[40:], maxDataLen+1) },
		"denied opcode":   func(p []byte) { binary.LittleEndian.PutUint32(p[44:], 0x04030000) },
		"tail after mbox": func(p []byte) { p[20+fMailbox+mboxSize] = 1 },
		"no buffer":       func(p []byte) { binary.LittleEndian.PutUint64(p[148:], 0) },
		"length mismatch": func(p []byte) { binary.LittleEndian.PutUint64(p[156:], 1) },
		"second buffer":   func(p []byte) { p[164] = 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := okPacket(opCtrlGetInfo)
			if mutate == nil {
				p = p[:100]
			} else {
				mutate(p)
			}

			assert.Error(t, checkPacket(p))
		})
	}
}

func TestEveryAllowedOpcodePassesAndEveryDeniedOneFails(t *testing.T) {
	require.Len(t, allowedOpcodes(), 19)
	for _, op := range allowedOpcodes() {
		assert.NoError(t, checkPacket(okPacket(op)), allowed[op])
	}
	for op, name := range denyTable {
		_, both := allowed[op]
		assert.False(t, both, name)
		assert.Error(t, checkPacket(okPacket(op)), name)
	}
}

func TestNoOtherOpcodePassesTheCheck(t *testing.T) {
	// Sweep the structured opcode space (group, command, sub-command, flags byte) around the real ones.
	for g := uint32(0); g <= 0x0A; g++ {
		for c := uint32(0); c <= 0x1F; c++ {
			for s := uint32(0); s <= 0xFF; s++ {
				for _, low := range []uint32{0, 1, 2} {
					op := g<<24 | c<<16 | s<<8 | low
					err := checkPacket(okPacket(op))
					_, isAllowed := allowed[op]
					if isAllowed {
						require.NoError(t, err, "%#08x", op)
					} else {
						require.Error(t, err, "%#08x", op)
					}
				}
			}
		}
	}
}

func FuzzCheckPacket(f *testing.F) {
	f.Add(uint32(opCtrlGetInfo), uint16(2048))
	f.Add(uint32(0x04030000), uint16(8))
	f.Fuzz(func(t *testing.T, op uint32, n uint16) {
		p := buildPacket(0, request{opcode: op, dataLen: int(n)}, 1)
		err := checkPacket(p)
		if err == nil {
			if _, ok := allowed[op]; !ok {
				t.Fatalf("opcode %#08x passed", op)
			}
		}
	})
}

// ---- pass-through ----

func passPacket(cdb [maxCDBLen]byte, cdbLen, dataLen int) []byte {
	return buildPassPacket(1, passRequest{target: 4, cdb: cdb, cdbLen: cdbLen, dataLen: dataLen}, 0x2000, 0x3000)
}

func TestPassPacketOffsetsAreGolden(t *testing.T) {
	cmd := smartdecode.IdentifyCommand()
	p := passPacket(ataCDB(cmd), 16, 512)

	le := binary.LittleEndian
	assert.Equal(t, uint32(48), le.Uint32(p[4:]), "sgl_off")
	assert.Equal(t, uint32(24), le.Uint32(p[12:]), "sense_off")
	assert.Equal(t, uint32(32), le.Uint32(p[16:]), "sense_len")
	f := p[20:148]
	assert.Equal(t, byte(4), f[0], "MFI_CMD_PD_SCSI_IO")
	assert.Equal(t, byte(4), f[4], "target")
	assert.Equal(t, byte(16), f[6], "cdb length")
	assert.Equal(t, uint32(512), le.Uint32(f[20:]))
	assert.Equal(t, uint64(0x3000), le.Uint64(f[24:]), "sense address")
	assert.Equal(t, byte(0x85), f[32], "cdb[0]")
	assert.Equal(t, byte(0xEC), f[32+14], "cdb[14]")
	require.NoError(t, checkPassPacket(p))
}

func TestPassPacketAllowsExactlyTheListedCommands(t *testing.T) {
	cmds := map[string]smartdecode.ATACommand{
		"identify":   smartdecode.IdentifyCommand(),
		"smart data": smartdecode.SmartReadDataCommand(),
		"thresholds": smartdecode.SmartReadThresholdsCommand(),
		"status":     smartdecode.SmartReturnStatusCommand(),
		"power mode": smartdecode.CheckPowerModeCommand(),
		"dev stats":  smartdecode.DeviceStatisticsCommand(5),
	}
	for name, cmd := range cmds {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, checkPassPacket(passPacket(ataCDB(cmd), 16, cmd.DataLen)))
		})
	}

	inq := inquiryCDB(96)
	assert.NoError(t, checkPassPacket(passPacket(inq, 6, 96)))
}

func TestEveryOtherAtaCommandAndScsiOpcodeIsRefused(t *testing.T) {
	passes := map[[2]uint8]bool{}
	// Every command byte and feature byte on top of the register layout of each allowed command.
	templates := []smartdecode.ATACommand{
		smartdecode.IdentifyCommand(), smartdecode.SmartReadDataCommand(), smartdecode.SmartReturnStatusCommand(),
		smartdecode.CheckPowerModeCommand(), smartdecode.DeviceStatisticsCommand(5),
	}
	for c := 0; c < 256; c++ {
		for feat := 0; feat < 256; feat++ {
			for _, cmd := range templates {
				cmd.Command, cmd.Features = uint8(c), uint8(feat)
				cdb := ataCDB(cmd)
				if checkPassCDB(cdb[:], 16, cmd.DataLen) == nil {
					passes[[2]uint8{uint8(c), uint8(feat)}] = true
				}
			}
		}
	}
	// IDENTIFY (EC), CHECK POWER MODE (E5), SMART data/thresholds/status (B0 D0, D1, DA) and READ LOG EXT (2F).
	want := map[[2]uint8]bool{{0xEC, 0}: true, {0xE5, 0}: true, {0xB0, 0xD0}: true, {0xB0, 0xD1}: true, {0xB0, 0xDA}: true, {0x2F, 0}: true}
	assert.Equal(t, want, passes)
	for _, c := range []uint8{0xD8, 0xD9, 0xD2, 0xD4} {
		cmd := smartdecode.ATACommand{Command: 0xB0, Features: c, LBAMid: 0x4F, LBAHigh: 0xC2, Protocol: smartdecode.ProtoNonData}
		cdb := ataCDB(cmd)
		assert.Error(t, checkPassCDB(cdb[:], 16, 0), "SMART feature %#x (enable, disable, autosave, off-line test)", c)
	}

	for op := 0; op < 256; op++ {
		var cdb [maxCDBLen]byte
		cdb[0] = byte(op)
		if op == scsiInquiry {
			cdb[4] = 96
			assert.NoError(t, checkPassCDB(cdb[:], 6, 96))
			continue
		}
		if op == ataPassThrough16 {
			continue
		}
		assert.Error(t, checkPassCDB(cdb[:], 16, 0), "SCSI opcode %#x", op)
	}
}

func TestPassPacketRefusesOtherFields(t *testing.T) {
	good := smartdecode.IdentifyCommand()
	tests := map[string]func(p []byte){
		"dcmd frame":      func(p []byte) { p[20] = 5 },
		"sense off":       func(p []byte) { p[12] = 0 },
		"sgl off":         func(p []byte) { p[4] = 40 },
		"write flag":      func(p []byte) { p[36] = 0x20 },
		"long transfer":   func(p []byte) { binary.LittleEndian.PutUint32(p[40:], 513) },
		"second buffer":   func(p []byte) { p[164] = 1 },
		"tail after cdb":  func(p []byte) { p[20+48] = 1 },
		"zero cdb length": func(p []byte) { p[26] = 0 },
		"lun":             func(p []byte) { p[25] = 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := passPacket(ataCDB(good), 16, 512)
			mutate(p)

			assert.Error(t, checkPassPacket(p))
		})
	}
	assert.Error(t, checkPassPacket(make([]byte, 10)))
}

func FuzzCheckPassCDB(f *testing.F) {
	cdb := ataCDB(smartdecode.IdentifyCommand())
	f.Add(cdb[:], 16, 512)
	f.Fuzz(func(t *testing.T, raw []byte, cdbLen, dataLen int) {
		if len(raw) != maxCDBLen {
			return
		}
		if checkPassCDB(raw, cdbLen, dataLen) != nil {
			return
		}
		switch raw[0] {
		case scsiInquiry:
		case ataPassThrough16:
			switch raw[14] {
			case 0xEC, 0xE5, 0xB0, 0x2F:
			default:
				t.Fatalf("ATA command %#x passed", raw[14])
			}
			if raw[14] == 0xB0 && raw[4] != 0xD0 && raw[4] != 0xD1 && raw[4] != 0xDA {
				t.Fatalf("SMART feature %#x passed", raw[4])
			}
		default:
			t.Fatalf("SCSI opcode %#x passed", raw[0])
		}
	})
}
