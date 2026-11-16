// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// NVMe admin opcodes and identifiers the decoders use.
const (
	nvmeOpGetLogPage = 0x02
	nvmeOpIdentify   = 0x06

	nvmeCNSController = 0x01
	nvmeLogSmart      = 0x02

	smartLogBytes = 512
	nvmeDataUnit  = 512000
)

// NVMeCommand is one NVMe admin command.
type NVMeCommand struct {
	Opcode uint8
	NSID   uint32
	CDW10  uint32
	CDW11  uint32
	// DataLen is the bytes the command returns.
	DataLen int
}

// NVMeTransport sends one allowed admin command to one controller and returns the data it gives back.
type NVMeTransport interface {
	Admin(cmd NVMeCommand) ([]byte, error)
}

// IdentifyControllerCommand is Identify with CNS 01h, the 4096 byte controller structure.
func IdentifyControllerCommand() NVMeCommand {
	return NVMeCommand{Opcode: nvmeOpIdentify, CDW10: nvmeCNSController, DataLen: 4096}
}

// SmartLogCommand is Get Log Page 02h, the 512 byte SMART/Health log of the whole controller.
func SmartLogCommand() NVMeCommand {
	numd := uint32(smartLogBytes/4 - 1)
	return NVMeCommand{Opcode: nvmeOpGetLogPage, NSID: 0xFFFFFFFF, CDW10: numd<<16 | nvmeLogSmart, DataLen: smartLogBytes}
}

// CheckNVMe returns nil for Identify Controller and the SMART/Health log page, and ErrCommandNotAllowed
// for anything else. A command passes only when every field equals the one its constructor sets, so the
// namespace, the command fields, the length of data asked for and the data length are pinned.
func CheckNVMe(cmd NVMeCommand) error {
	switch cmd {
	case IdentifyControllerCommand(), SmartLogCommand():
		return nil
	}
	return fmt.Errorf("%w: NVMe admin opcode %#02x CDW10 %#08x", ErrCommandNotAllowed, cmd.Opcode, cmd.CDW10)
}

// NVMeIdentify holds what Identify Controller tells.
type NVMeIdentify struct {
	Model     string
	Serial    string
	Firmware  string
	SizeBytes uint64
}

func trimField(b []byte) string { return strings.Trim(string(b), " \x00") }

// ParseNVMeIdentify decodes a 4096 byte Identify Controller answer.
func ParseNVMeIdentify(buf []byte) (NVMeIdentify, error) {
	if len(buf) < 4096 {
		return NVMeIdentify{}, fmt.Errorf("%w: Identify Controller gave %d bytes, want 4096", ErrShortBuffer, len(buf))
	}
	return NVMeIdentify{
		Serial:    trimField(buf[4:24]),
		Model:     trimField(buf[24:64]),
		Firmware:  trimField(buf[64:72]),
		SizeBytes: binary.LittleEndian.Uint64(buf[280:288]),
	}, nil
}

// NVMeSensor is one temperature sensor of the log page, numbered from 1.
type NVMeSensor struct {
	Index   int
	Celsius int
}

// NVMeSmart holds the SMART/Health log page.
type NVMeSmart struct {
	CriticalWarning byte
	// CompositeCelsius is meaningful only when HasComposite is true; a controller that reports 0 K has
	// no composite reading.
	CompositeCelsius int
	HasComposite     bool
	SensorsCelsius   []NVMeSensor
	AvailableSpare   uint8
	SpareThreshold   uint8
	PercentageUsed   uint8
	DataUnitsRead    uint64
	DataUnitsWritten uint64
	PowerCycles      uint64
	PowerOnHours     uint64
	UnsafeShutdowns  uint64
	MediaErrors      uint64
}

// BytesRead is the data read in bytes; a data unit is 512,000 bytes. It saturates at the largest
// 64-bit value.
func (s NVMeSmart) BytesRead() uint64 { return unitsToBytes(s.DataUnitsRead) }

// BytesWritten is the data written in bytes, saturating like BytesRead.
func (s NVMeSmart) BytesWritten() uint64 { return unitsToBytes(s.DataUnitsWritten) }

func unitsToBytes(units uint64) uint64 {
	if units > ^uint64(0)/nvmeDataUnit {
		return ^uint64(0)
	}
	return units * nvmeDataUnit
}

// counter128 reads a 128-bit little-endian counter and saturates at the largest 64-bit value.
func counter128(b []byte) uint64 {
	if binary.LittleEndian.Uint64(b[8:16]) != 0 {
		return ^uint64(0)
	}
	return binary.LittleEndian.Uint64(b[:8])
}

// ParseNVMeSmartLog decodes the 512 byte SMART/Health log page.
func ParseNVMeSmartLog(buf []byte) (NVMeSmart, error) {
	if len(buf) < smartLogBytes {
		return NVMeSmart{}, fmt.Errorf("%w: SMART/Health log gave %d bytes, want %d", ErrShortBuffer, len(buf), smartLogBytes)
	}
	composite := binary.LittleEndian.Uint16(buf[1:3])
	out := NVMeSmart{
		CriticalWarning:  buf[0],
		CompositeCelsius: int(composite) - 273,
		HasComposite:     composite != 0,
		AvailableSpare:   buf[3],
		SpareThreshold:   buf[4],
		PercentageUsed:   buf[5],
		DataUnitsRead:    counter128(buf[32:48]),
		DataUnitsWritten: counter128(buf[48:64]),
		PowerCycles:      counter128(buf[112:128]),
		PowerOnHours:     counter128(buf[128:144]),
		UnsafeShutdowns:  counter128(buf[144:160]),
		MediaErrors:      counter128(buf[160:176]),
	}
	for i := 0; i < 8; i++ {
		if k := binary.LittleEndian.Uint16(buf[200+2*i:]); k != 0 {
			out.SensorsCelsius = append(out.SensorsCelsius, NVMeSensor{Index: i + 1, Celsius: int(k) - 273})
		}
	}
	return out, nil
}

// NVMeState maps the critical warning byte to a hw.state value: bit 3 (read-only) is failed, bits 0
// and 2 are predicted_failure, bits 1, 4 and 5 are degraded, and the most severe set bit decides.
func NVMeState(warning byte) string {
	switch {
	case warning&0x08 != 0:
		return "failed"
	case warning&0x05 != 0:
		return "predicted_failure"
	case warning&0x32 != 0:
		return "degraded"
	}
	return "ok"
}
