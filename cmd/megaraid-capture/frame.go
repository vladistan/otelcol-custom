// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"fmt"
)

// The ioctl packet is struct megasas_iocpacket of the Linux driver header megaraid_sas.h:
// host_no u16, pad u16, sgl_off u32, sge_count u32, sense_off u32, sense_len u32, a 128-byte
// frame, then 16 iovec entries of 16 bytes. The driver does not bounds-check sgl_off, so every
// offset below is a constant, never read from input.
const (
	packetSize  = 404
	frameOff    = 20
	frameSize   = 128
	sglOff      = 40
	sglArrayOff = frameOff + frameSize
	iovecSize   = 16
	maxSGE      = 16
	maxDataLen  = 64 * 1024

	cmdDCMD   = 5
	flagsRead = 0x0010

	// ioctlFirmware is MEGASAS_IOC_FIRMWARE, _IOWR('M', 1, 404 bytes).
	ioctlFirmware uintptr = 0xC1944D01

	// Offsets inside the frame (struct megasas_header and the DCMD frame of mfireg.h).
	fCmd      = 0
	fSenseLen = 1
	fStatus   = 2
	fSCSI     = 3
	fTarget   = 4
	fLUN      = 5
	fCDBLen   = 6
	fSGECount = 7
	fContext  = 8
	fFlags    = 16
	fTimeout  = 18
	fDataLen  = 20
	fOpcode   = 24
	fMailbox  = 28
	mboxSize  = 12
)

// request is one DCMD read: an opcode, its mailbox and the size of the buffer for the answer.
type request struct {
	opcode  uint32
	mbox    [mboxSize]byte
	dataLen int
}

// buildPacket returns the ioctl packet for a DCMD read whose answer goes to the buffer at buf.
func buildPacket(hostNo uint16, req request, buf uintptr) []byte {
	p := make([]byte, packetSize)
	le := binary.LittleEndian
	le.PutUint16(p[0:], hostNo)
	le.PutUint32(p[4:], sglOff)
	le.PutUint32(p[8:], 1)
	f := p[frameOff : frameOff+frameSize]
	f[fCmd] = cmdDCMD
	f[fSGECount] = 1
	le.PutUint16(f[fFlags:], flagsRead)
	le.PutUint32(f[fDataLen:], uint32(req.dataLen)) //nolint:gosec // checkPacket bounds it
	le.PutUint32(f[fOpcode:], req.opcode)
	copy(f[fMailbox:fMailbox+mboxSize], req.mbox[:])
	le.PutUint64(p[sglArrayOff:], uint64(buf))
	le.PutUint64(p[sglArrayOff+8:], uint64(req.dataLen)) //nolint:gosec // checkPacket bounds it
	return p
}

// packetStatus is the command status the driver copies back into the frame.
func packetStatus(p []byte) uint8 { return p[frameOff+fStatus] }

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// checkPacket is the one function every frame passes before it reaches the kernel. It accepts
// only a DCMD read of an allowed opcode with one data buffer and nothing else set.
func checkPacket(p []byte) error {
	if len(p) != packetSize {
		return fmt.Errorf("packet is %d bytes, want %d", len(p), packetSize)
	}
	le := binary.LittleEndian
	if le.Uint16(p[2:]) != 0 {
		return fmt.Errorf("packet padding is set")
	}
	if le.Uint32(p[4:]) != sglOff {
		return fmt.Errorf("sgl_off is %d, want %d", le.Uint32(p[4:]), sglOff)
	}
	if le.Uint32(p[8:]) != 1 {
		return fmt.Errorf("sge_count is %d, want 1", le.Uint32(p[8:]))
	}
	if le.Uint32(p[12:]) != 0 || le.Uint32(p[16:]) != 0 {
		return fmt.Errorf("sense buffer is set")
	}
	f := p[frameOff : frameOff+frameSize]
	if f[fCmd] != cmdDCMD {
		return fmt.Errorf("command %d is not a DCMD", f[fCmd])
	}
	if f[fSenseLen] != 0 || f[fStatus] != 0 || f[fSCSI] != 0 || f[fTarget] != 0 || f[fLUN] != 0 || f[fCDBLen] != 0 {
		return fmt.Errorf("frame header carries fields a DCMD read does not use")
	}
	if f[fSGECount] != 1 {
		return fmt.Errorf("frame sge_count is %d, want 1", f[fSGECount])
	}
	if !allZero(f[fContext : fContext+8]) {
		return fmt.Errorf("frame context is set")
	}
	if flags := le.Uint16(f[fFlags:]); flags != flagsRead {
		return fmt.Errorf("frame flags %#x are not a plain read", flags)
	}
	if le.Uint16(f[fTimeout:]) != 0 {
		return fmt.Errorf("frame timeout is set")
	}
	n := le.Uint32(f[fDataLen:])
	if n == 0 || n > maxDataLen {
		return fmt.Errorf("transfer length %d is outside 1..%d", n, maxDataLen)
	}
	opcode := le.Uint32(f[fOpcode:])
	if _, ok := allowed[opcode]; !ok {
		return fmt.Errorf("opcode %#08x is not on the allow-list", opcode)
	}
	if !allZero(f[fMailbox+mboxSize:]) {
		return fmt.Errorf("frame bytes after the mailbox are set")
	}
	if le.Uint64(p[sglArrayOff:]) == 0 || le.Uint64(p[sglArrayOff+8:]) != uint64(n) {
		return fmt.Errorf("data buffer entry does not match the transfer length")
	}
	if !allZero(p[sglArrayOff+iovecSize:]) {
		return fmt.Errorf("more than one data buffer entry is used")
	}
	return nil
}
