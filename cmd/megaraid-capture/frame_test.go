// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"testing"
)

// ioctlNumber is _IOWR(type, nr, size) on Linux: direction 3 in bits 30-31, size in 16-29.
func ioctlNumber(typ byte, nr, size uint32) uint32 {
	return 3<<30 | size<<16 | uint32(typ)<<8 | nr
}

func TestPacketSizeAndRequestNumber(t *testing.T) {
	if packetSize != 404 {
		t.Fatalf("packetSize = %d, want 404", packetSize)
	}
	if got := ioctlNumber('M', 1, 404); got != 0xC1944D01 {
		t.Fatalf("_IOWR('M', 1, 404) = %#x, want 0xC1944D01", got)
	}
	if uint32(ioctlFirmware) != 0xC1944D01 {
		t.Fatalf("ioctlFirmware = %#x, want 0xC1944D01", uint32(ioctlFirmware))
	}
}

func TestFrameConstantsArePinned(t *testing.T) {
	pins := map[string][2]int{
		"frameOff":    {frameOff, 20},
		"frameSize":   {frameSize, 128},
		"sglOff":      {sglOff, 40},
		"sglArrayOff": {sglArrayOff, 148},
		"iovecSize":   {iovecSize, 16},
		"maxSGE":      {maxSGE, 16},
		"cmdDCMD":     {cmdDCMD, 5},
		"flagsRead":   {flagsRead, 0x10},
		"maxDataLen":  {maxDataLen, 65536},
	}
	for name, p := range pins {
		if p[0] != p[1] {
			t.Errorf("%s = %d, want %d", name, p[0], p[1])
		}
	}
	if sglArrayOff+maxSGE*iovecSize != packetSize {
		t.Errorf("sgl array end %d != packetSize %d", sglArrayOff+maxSGE*iovecSize, packetSize)
	}
}

// goldenPacket is the packet for CTRL_GETINFO on host 3 with a 2048-byte buffer at address 0x1000,
// written out byte by byte from the megasas_iocpacket layout, not from the offsets in the code.
func goldenPacket() []byte {
	p := make([]byte, 404)
	binary.LittleEndian.PutUint16(p[0:], 3)        // host_no
	binary.LittleEndian.PutUint32(p[4:], 40)       // sgl_off
	binary.LittleEndian.PutUint32(p[8:], 1)        // sge_count
	p[20+0] = 5                                    // frame: cmd = DCMD
	p[20+7] = 1                                    // frame: sge_count
	binary.LittleEndian.PutUint16(p[20+16:], 0x10) // frame: flags = read
	binary.LittleEndian.PutUint32(p[20+20:], 2048) // frame: data_xfer_len
	binary.LittleEndian.PutUint32(p[20+24:], 0x01010000)
	binary.LittleEndian.PutUint64(p[148:], 0x1000) // iovec[0].base
	binary.LittleEndian.PutUint64(p[156:], 2048)   // iovec[0].len
	return p
}

func TestBuildPacketMatchesTheGoldenBytes(t *testing.T) {
	got := buildPacket(3, request{opcode: 0x01010000, dataLen: 2048}, 0x1000)

	if !bytes.Equal(got, goldenPacket()) {
		t.Fatalf("packet differs\n got %s\nwant %s", hex.EncodeToString(got), hex.EncodeToString(goldenPacket()))
	}
}

func TestBuildPacketPlacesTheMailboxAtFrameOffset28(t *testing.T) {
	var mbox [12]byte
	for i := range mbox {
		mbox[i] = byte(0xA0 + i)
	}

	got := buildPacket(0, request{opcode: 0x02020000, mbox: mbox, dataLen: 512}, 0x2000)

	if !bytes.Equal(got[20+28:20+40], mbox[:]) {
		t.Fatalf("mailbox bytes = %x, want %x", got[20+28:20+40], mbox)
	}
	if err := checkPacket(got); err != nil {
		t.Fatalf("a built PD_GET_INFO packet must pass the check: %v", err)
	}
}

func TestPacketStatusReadsFrameOffset2(t *testing.T) {
	p := buildPacket(0, request{opcode: 0x01010000, dataLen: 8}, 0x1000)
	p[frameOff+2] = 12

	if got := packetStatus(p); got != 12 {
		t.Fatalf("status = %d, want 12", got)
	}
}

func TestCheckPacketRefusesAnyDeviationFromTheReadOnlyShape(t *testing.T) {
	base := func() []byte { return buildPacket(1, request{opcode: 0x01010000, dataLen: 2048}, 0x1000) }
	mutations := map[string]func([]byte){
		"short packet":          nil,
		"command is not DCMD":   func(p []byte) { p[frameOff] = 4 },
		"pass-through command":  func(p []byte) { p[frameOff] = 4; p[frameOff+6] = 16 },
		"data out flag":         func(p []byte) { binary.LittleEndian.PutUint16(p[frameOff+16:], 0x08) },
		"both directions":       func(p []byte) { binary.LittleEndian.PutUint16(p[frameOff+16:], 0x18) },
		"no direction":          func(p []byte) { binary.LittleEndian.PutUint16(p[frameOff+16:], 0) },
		"extra flag bit":        func(p []byte) { binary.LittleEndian.PutUint16(p[frameOff+16:], 0x12) },
		"sgl offset moved":      func(p []byte) { binary.LittleEndian.PutUint32(p[4:], 48) },
		"two sges":              func(p []byte) { binary.LittleEndian.PutUint32(p[8:], 2) },
		"no sge":                func(p []byte) { binary.LittleEndian.PutUint32(p[8:], 0) },
		"frame sge count":       func(p []byte) { p[frameOff+7] = 2 },
		"sense offset set":      func(p []byte) { binary.LittleEndian.PutUint32(p[12:], 60) },
		"sense length set":      func(p []byte) { binary.LittleEndian.PutUint32(p[16:], 32) },
		"cdb length set":        func(p []byte) { p[frameOff+6] = 6 },
		"target id set":         func(p []byte) { p[frameOff+4] = 1 },
		"opcode not allowed":    func(p []byte) { binary.LittleEndian.PutUint32(p[frameOff+24:], 0x04030000) },
		"transfer length zero":  func(p []byte) { binary.LittleEndian.PutUint32(p[frameOff+20:], 0) },
		"transfer over limit":   func(p []byte) { binary.LittleEndian.PutUint32(p[frameOff+20:], maxDataLen+1) },
		"iovec length differs":  func(p []byte) { binary.LittleEndian.PutUint64(p[sglArrayOff+8:], 4096) },
		"second iovec used":     func(p []byte) { binary.LittleEndian.PutUint64(p[sglArrayOff+iovecSize+8:], 8) },
		"iovec base is zero":    func(p []byte) { binary.LittleEndian.PutUint64(p[sglArrayOff:], 0) },
		"context is set":        func(p []byte) { p[frameOff+8] = 1 },
		"frame tail not zero":   func(p []byte) { p[frameOff+100] = 1 },
		"timeout set":           func(p []byte) { binary.LittleEndian.PutUint16(p[frameOff+18:], 5) },
		"scsi status set":       func(p []byte) { p[frameOff+3] = 2 },
		"lun set":               func(p []byte) { p[frameOff+5] = 1 },
		"cmd status preloaded":  func(p []byte) { p[frameOff+2] = 0xff },
		"frame sense byte":      func(p []byte) { p[frameOff+1] = 3 },
		"pad field set":         func(p []byte) { p[2] = 1 },
		"unused iovec nonzero":  func(p []byte) { p[sglArrayOff+5*iovecSize] = 7 },
		"mailbox is not opcode": nil,
	}
	if err := checkPacket(base()); err != nil {
		t.Fatalf("the unmodified packet must pass: %v", err)
	}
	if err := checkPacket(base()[:403]); err == nil {
		t.Error("a short packet must be refused")
	}
	for name, mutate := range mutations {
		if mutate == nil {
			continue
		}
		p := base()
		mutate(p)
		if err := checkPacket(p); err == nil {
			t.Errorf("%s: packet passed the check", name)
		}
	}
}

func TestCheckPacketPassesExactlyTheAllowedOpcodesUnderRandomMutation(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200000; i++ {
		opcode := rng.Uint32()
		if i%4 == 0 {
			opcode = allowedOpcodes()[rng.Intn(len(allowedOpcodes()))]
		}
		p := buildPacket(uint16(rng.Intn(4)), request{opcode: opcode, dataLen: 1 + rng.Intn(maxDataLen)}, 0x1000)
		err := checkPacket(p)
		_, isAllowed := allowed[opcode]
		if isAllowed != (err == nil) {
			t.Fatalf("opcode %#x: allowed=%v, check error=%v", opcode, isAllowed, err)
		}
	}
}
