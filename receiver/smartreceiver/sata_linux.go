// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

const (
	// sgIO is SG_IO, the SCSI generic ioctl; x/sys/unix has no name for it.
	sgIO = 0x2285

	sgDxferNone       = -1
	sgDxferFromDev    = -3
	sgInterfaceID     = 'S'
	senseBufferLen    = 32
	scsiCheckCond     = 0x02
	didTimeOut        = 0x03
	driverTimeout     = 0x06
	driverStatusMask  = 0x0F
	ataPassThrough16  = 0x85
	protoNonData      = 3
	protoPIOIn        = 4
	extendBit         = 0x01 // byte 1 bit 0: the command is a 48-bit one
	flagCheckCond     = 0x20
	flagPIOInTransfer = 0x0E // T_DIR from device, BYTE_BLOCK blocks, T_LENGTH in the sector count
	deviceLBA         = 0x40
)

// timeoutGuard is how long past the command timeout the transport waits for an ioctl that the kernel
// did not end. Tests shorten it.
var timeoutGuard = 2 * time.Second

// sgIOHdr is struct sg_io_hdr of <scsi/sg.h>. The Go layout follows the C one on 32 and 64 bit hosts.
type sgIOHdr struct {
	interfaceID    int32
	dxferDirection int32
	cmdLen         uint8
	mxSbLen        uint8
	iovecCount     uint16
	dxferLen       uint32
	dxferp         unsafe.Pointer
	cmdp           unsafe.Pointer
	sbp            unsafe.Pointer
	timeout        uint32
	flags          uint32
	packID         int32
	usrPtr         unsafe.Pointer
	status         uint8
	maskedStatus   uint8
	msgStatus      uint8
	sbLenWr        uint8
	hostStatus     uint16
	driverStatus   uint16
	resid          int32
	duration       uint32
	info           uint32
}

// sgTransport sends allow-listed ATA commands to one disk as ATA PASS-THROUGH(16) through SG_IO.
type sgTransport struct {
	fd      int
	timeout time.Duration
	ioctl   ioctlFunc
	// inflight is shared by every transport opened for the same device; nil in tests that do not need it.
	inflight *inflight
}

// ataCDB builds the 16 byte ATA PASS-THROUGH CDB of a command.
func ataCDB(cmd smartdecode.ATACommand) [16]byte {
	var cdb [16]byte
	cdb[0] = ataPassThrough16
	if cmd.Protocol == smartdecode.ProtoPIOIn {
		cdb[1] = protoPIOIn << 1
		cdb[2] = flagPIOInTransfer
	} else {
		cdb[1] = protoNonData << 1
	}
	if cmd.WantRegisters {
		cdb[2] |= flagCheckCond
	}
	if cmd.Is48Bit() {
		cdb[1] |= extendBit // the high register bytes, 3, 5, 7, 9 and 11, stay zero
	}
	cdb[4] = cmd.Features
	cdb[6] = cmd.SectorCount
	cdb[8] = cmd.LBALow
	cdb[10] = cmd.LBAMid
	cdb[12] = cmd.LBAHigh
	cdb[13] = cmd.Device | deviceLBA
	cdb[14] = cmd.Command
	return cdb
}

// ATA implements smartdecode.ATATransport. The allow list is checked again here, so no caller can send
// another command through this type.
func (t *sgTransport) ATA(cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	if err := smartdecode.CheckATA(cmd); err != nil {
		return smartdecode.ATAResult{}, err
	}
	cdb := ataCDB(cmd)
	data := make([]byte, cmd.DataLen)
	sense := make([]byte, senseBufferLen)
	hdr := sgIOHdr{
		interfaceID: sgInterfaceID,
		cmdLen:      uint8(len(cdb)),
		mxSbLen:     uint8(len(sense)),
		cmdp:        unsafe.Pointer(&cdb[0]),
		sbp:         unsafe.Pointer(&sense[0]),
		timeout:     uint32(t.timeout / time.Millisecond),
	}
	if cmd.DataLen > 0 {
		hdr.dxferDirection = sgDxferFromDev
		hdr.dxferLen = uint32(cmd.DataLen)
		hdr.dxferp = unsafe.Pointer(&data[0])
	} else {
		hdr.dxferDirection = sgDxferNone
	}

	if !t.inflight.acquire() {
		return smartdecode.ATAResult{}, errStillRunning()
	}
	done := make(chan error, 1)
	go func() {
		_, err := t.ioctl(t.fd, sgIO, unsafe.Pointer(&hdr))
		// The goroutine owns the buffers until the ioctl returns, also after the caller gave up.
		runtime.KeepAlive(cdb)
		runtime.KeepAlive(data)
		runtime.KeepAlive(sense)
		t.inflight.release()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return smartdecode.ATAResult{}, mapErrno(err)
		}
	case <-time.After(t.timeout + timeoutGuard):
		return smartdecode.ATAResult{}, fmt.Errorf("%w: SG_IO did not return", smartdecode.ErrTimeout)
	}
	runtime.KeepAlive(cdb)
	runtime.KeepAlive(data)
	runtime.KeepAlive(sense)
	return sgResult(&hdr, data, sense, cmd)
}

// sgResult turns the header the kernel filled into a result or an error.
func sgResult(hdr *sgIOHdr, data, sense []byte, cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	if hdr.hostStatus == didTimeOut || hdr.driverStatus&driverStatusMask == driverTimeout {
		return smartdecode.ATAResult{}, smartdecode.ErrTimeout
	}
	if hdr.hostStatus != 0 {
		return smartdecode.ATAResult{}, fmt.Errorf("SG_IO host status %#x", hdr.hostStatus)
	}
	var out smartdecode.ATAResult
	if hdr.sbLenWr > 0 {
		out.Registers, out.HaveRegisters = smartdecode.ParseATASense(sense[:hdr.sbLenWr])
	}
	switch {
	case hdr.status == 0:
	case hdr.status == scsiCheckCond && out.HaveRegisters:
		// SAT reports the registers of a command that asked for them as a check condition.
	case hdr.status == scsiCheckCond:
		return smartdecode.ATAResult{}, fmt.Errorf("%w: SCSI check condition, sense key %#x", smartdecode.ErrUnsupported, senseKey(sense, hdr.sbLenWr))
	default:
		return smartdecode.ATAResult{}, fmt.Errorf("SG_IO SCSI status %#x", hdr.status)
	}
	if cmd.DataLen > 0 {
		n := cmd.DataLen - int(hdr.resid)
		if n < 0 || n > len(data) {
			n = len(data)
		}
		out.Data = data[:n]
	}
	return out, nil
}

// senseKey reads the sense key of fixed or descriptor format sense data.
func senseKey(sense []byte, written uint8) uint8 {
	if written < 3 || len(sense) < 3 {
		return 0
	}
	return sense[2] & 0x0F
}
