// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// ATA command bytes and SMART feature codes the decoders use.
const (
	cmdIdentify       = 0xEC
	cmdSmart          = 0xB0
	cmdCheckPowerMode = 0xE5
	cmdReadLogExt     = 0x2F

	featReadData       = 0xD0
	featReadThresholds = 0xD1
	featReturnStatus   = 0xDA

	smartLBAMid  = 0x4F
	smartLBAHigh = 0xC2

	logDeviceStatistics = 0x04
	pageTemperature     = 0x05
)

// Protocol is how a command moves data.
type Protocol uint8

// The two protocols the allow list needs.
const (
	ProtoNonData Protocol = iota
	ProtoPIOIn
)

// ATACommand is one ATA command and its registers. A transport turns it into ATA PASS-THROUGH, or into
// the call a RAID controller offers.
type ATACommand struct {
	Command     uint8
	Features    uint8
	SectorCount uint8
	LBALow      uint8
	LBAMid      uint8
	LBAHigh     uint8
	Device      uint8
	Protocol    Protocol
	// DataLen is the bytes the command returns.
	DataLen int
	// WantRegisters asks the transport to return the registers the device sets.
	WantRegisters bool
}

// Is48Bit is true for READ LOG EXT, the one allowed command that is a 48-bit command; a transport sets
// the EXTEND flag for it.
func (c ATACommand) Is48Bit() bool { return c.Command == cmdReadLogExt }

// ATARegisters are the registers a device returns.
type ATARegisters struct {
	Error       uint8
	SectorCount uint8
	LBALow      uint8
	LBAMid      uint8
	LBAHigh     uint8
	Device      uint8
	Status      uint8
}

// ATAResult is the answer to one command.
type ATAResult struct {
	Data          []byte
	Registers     ATARegisters
	HaveRegisters bool
}

// ATATransport sends one allowed command to one device.
type ATATransport interface {
	ATA(cmd ATACommand) (ATAResult, error)
}

// IdentifyCommand is IDENTIFY DEVICE.
func IdentifyCommand() ATACommand {
	return ATACommand{Command: cmdIdentify, Protocol: ProtoPIOIn, SectorCount: 1, DataLen: 512}
}

// SmartReadDataCommand is SMART READ DATA.
func SmartReadDataCommand() ATACommand {
	return ATACommand{Command: cmdSmart, Features: featReadData, SectorCount: 1, LBAMid: smartLBAMid, LBAHigh: smartLBAHigh, Protocol: ProtoPIOIn, DataLen: 512}
}

// SmartReadThresholdsCommand is SMART READ THRESHOLDS.
func SmartReadThresholdsCommand() ATACommand {
	return ATACommand{Command: cmdSmart, Features: featReadThresholds, SectorCount: 1, LBAMid: smartLBAMid, LBAHigh: smartLBAHigh, Protocol: ProtoPIOIn, DataLen: 512}
}

// SmartReturnStatusCommand is SMART RETURN STATUS; the answer is in the registers.
func SmartReturnStatusCommand() ATACommand {
	return ATACommand{Command: cmdSmart, Features: featReturnStatus, LBAMid: smartLBAMid, LBAHigh: smartLBAHigh, Protocol: ProtoNonData, WantRegisters: true}
}

// CheckPowerModeCommand is CHECK POWER MODE; the answer is in the sector count register.
func CheckPowerModeCommand() ATACommand {
	return ATACommand{Command: cmdCheckPowerMode, Protocol: ProtoNonData, WantRegisters: true}
}

// DeviceStatisticsCommand is READ LOG EXT of one page of the Device Statistics log.
func DeviceStatisticsCommand(page uint8) ATACommand {
	return ATACommand{Command: cmdReadLogExt, SectorCount: 1, LBALow: logDeviceStatistics, LBAMid: page, Protocol: ProtoPIOIn, DataLen: 512}
}

// CheckATA returns nil for the six read-only commands the decoders send and ErrCommandNotAllowed for
// anything else. A command passes only when every field equals the one its constructor sets (READ LOG
// EXT may name any Device Statistics page), so the data length, the protocol and every register are
// pinned. SMART ENABLE, DISABLE, AUTOSAVE and the off-line tests are not on the list.
func CheckATA(cmd ATACommand) error {
	switch cmd {
	case IdentifyCommand(), SmartReadDataCommand(), SmartReadThresholdsCommand(),
		SmartReturnStatusCommand(), CheckPowerModeCommand(), DeviceStatisticsCommand(cmd.LBAMid):
		return nil
	}
	return fmt.Errorf("%w: ATA command %#02x features %#02x", ErrCommandNotAllowed, cmd.Command, cmd.Features)
}

// Identify holds what IDENTIFY DEVICE tells.
type Identify struct {
	Model          string
	Serial         string
	Firmware       string
	WWN            string
	SizeBytes      uint64
	RotationRate   uint16
	SMARTSupported bool
	SMARTEnabled   bool
}

// IsSSD is true for a rotation rate of 1, which means a device that does not rotate.
func (i Identify) IsSSD() bool { return i.RotationRate == 1 }

func word(buf []byte, n int) uint16 { return binary.LittleEndian.Uint16(buf[n*2:]) }

// ataString reads identify words first..last: two characters a word, the first in the high byte.
func ataString(buf []byte, first, last int) string {
	var out strings.Builder
	for w := first; w <= last; w++ {
		v := word(buf, w)
		out.WriteByte(byte(v >> 8))
		out.WriteByte(byte(v))
	}
	return validUTF8(strings.Trim(out.String(), " \x00"))
}

// ParseIdentify decodes a 512 byte IDENTIFY DEVICE answer.
func ParseIdentify(buf []byte) (Identify, error) {
	if len(buf) < 512 {
		return Identify{}, fmt.Errorf("%w: IDENTIFY DEVICE gave %d bytes, want 512", ErrShortBuffer, len(buf))
	}
	id := Identify{
		Model:          ataString(buf, 27, 46),
		Serial:         ataString(buf, 10, 19),
		Firmware:       ataString(buf, 23, 26),
		RotationRate:   word(buf, 217),
		SMARTSupported: word(buf, 82)&1 != 0,
		SMARTEnabled:   word(buf, 85)&1 != 0,
	}
	sectors := uint64(binary.LittleEndian.Uint32(buf[60*2:]))
	if word(buf, 83)&(1<<10) != 0 {
		sectors = binary.LittleEndian.Uint64(buf[100*2:])
	}
	sectorBytes := uint64(512)
	if w := word(buf, 106); w&(1<<14) != 0 && w&(1<<15) == 0 && w&(1<<12) != 0 {
		if words := binary.LittleEndian.Uint32(buf[117*2:]); words != 0 {
			sectorBytes = uint64(words) * 2
		}
	}
	id.SizeBytes = sectors * sectorBytes
	if w := word(buf, 87); w&(1<<14) != 0 && w&(1<<8) != 0 {
		wwn := uint64(word(buf, 108))<<48 | uint64(word(buf, 109))<<32 | uint64(word(buf, 110))<<16 | uint64(word(buf, 111))
		if wwn != 0 {
			id.WWN = fmt.Sprintf("%016x", wwn)
		}
	}
	return id, nil
}

// Attribute is one entry of the SMART attribute table.
type Attribute struct {
	ID           uint8
	Flags        uint16
	Value        uint8
	Worst        uint8
	Threshold    uint8
	HasThreshold bool
	// Raw is the 48-bit raw value.
	Raw uint64
}

const (
	attrTableOffset = 2
	attrEntryLen    = 12
	attrMaxEntries  = 30
)

// ParseSmartData decodes the SMART READ DATA table and merges the READ THRESHOLDS table when thresholds
// is not nil. Entries with id 0 are unused.
func ParseSmartData(data, thresholds []byte) ([]Attribute, error) {
	if len(data) < 512 {
		return nil, fmt.Errorf("%w: SMART READ DATA gave %d bytes, want 512", ErrShortBuffer, len(data))
	}
	limits := map[uint8]uint8{}
	if len(thresholds) >= 512 {
		for i := 0; i < attrMaxEntries; i++ {
			off := attrTableOffset + attrEntryLen*i
			if id := thresholds[off]; id != 0 {
				limits[id] = thresholds[off+1]
			}
		}
	}
	var out []Attribute
	for i := 0; i < attrMaxEntries; i++ {
		off := attrTableOffset + attrEntryLen*i
		id := data[off]
		if id == 0 {
			continue
		}
		var raw uint64
		for b := 0; b < 6; b++ {
			raw |= uint64(data[off+5+b]) << (8 * b)
		}
		attr := Attribute{
			ID:    id,
			Flags: binary.LittleEndian.Uint16(data[off+1:]),
			Value: data[off+3],
			Worst: data[off+4],
			Raw:   raw,
		}
		if limit, ok := limits[id]; ok {
			attr.Threshold, attr.HasThreshold = limit, true
		}
		out = append(out, attr)
	}
	return out, nil
}

// Health is the answer of SMART RETURN STATUS.
type Health int

// The two answers the device gives, and the value for no answer.
const (
	HealthUnknown Health = iota
	HealthOK
	HealthPredictedFailure
)

// ParseReturnStatus reads the LBA mid and high registers of SMART RETURN STATUS.
func ParseReturnStatus(r ATARegisters) (Health, error) {
	switch {
	case r.LBAMid == 0x4F && r.LBAHigh == 0xC2:
		return HealthOK, nil
	case r.LBAMid == 0xF4 && r.LBAHigh == 0x2C:
		return HealthPredictedFailure, nil
	}
	return HealthUnknown, fmt.Errorf("SMART RETURN STATUS gave registers %#02x/%#02x, neither healthy nor failing", r.LBAMid, r.LBAHigh)
}

// ParseATASense reads the ATA Status Return descriptor of descriptor-format sense data, where SAT
// returns the registers of a command that asked for them.
func ParseATASense(sense []byte) (ATARegisters, bool) {
	if len(sense) < 8 || (sense[0]&0x7F != 0x72 && sense[0]&0x7F != 0x73) {
		return ATARegisters{}, false
	}
	end := 8 + int(sense[7])
	if end > len(sense) {
		end = len(sense)
	}
	for off := 8; off+2 <= end; {
		length := int(sense[off+1]) + 2
		if sense[off] == 0x09 && length >= 14 && off+14 <= len(sense) {
			d := sense[off:]
			return ATARegisters{
				Error: d[3], SectorCount: d[5], LBALow: d[7], LBAMid: d[9], LBAHigh: d[11], Device: d[12], Status: d[13],
			}, true
		}
		off += length
	}
	return ATARegisters{}, false
}

// ParseDeviceStatisticsTemperature reads the current temperature of the Temperature Statistics page of
// the Device Statistics log. The value counts only when the supported and valid flags are set.
func ParseDeviceStatisticsTemperature(page []byte) (int, bool) {
	if len(page) < 16 {
		return 0, false
	}
	flags := page[15]
	if flags&0x80 == 0 || flags&0x40 == 0 {
		return 0, false
	}
	return int(int8(page[8])), true
}
