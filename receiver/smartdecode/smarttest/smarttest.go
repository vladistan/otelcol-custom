// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package smarttest builds the buffers SATA and NVMe disks return and gives fake transports that
// answer from them. The tests of the decoders and of the receivers that use them share it; no
// production code imports it. The buffers follow the layouts of the standards, as no disk was read to
// make them.
package smarttest

import (
	"encoding/binary"
	"errors"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func putATAString(buf []byte, firstWord, lastWord int, s string) {
	n := (lastWord - firstWord + 1) * 2
	padded := []byte(s)
	for len(padded) < n {
		padded = append(padded, ' ')
	}
	for i := 0; i < n; i += 2 {
		buf[firstWord*2+i] = padded[i+1]
		buf[firstWord*2+i+1] = padded[i]
	}
}

func putWord(buf []byte, word int, v uint16) { binary.LittleEndian.PutUint16(buf[word*2:], v) }

// IdentifySpec describes a disk for IdentifyBuffer.
type IdentifySpec struct {
	Model, Serial, Firmware string
	Sectors                 uint64
	Rotation                uint16
	WWN                     uint64
	SMARTSupported          bool
	SMARTEnabled            bool
}

// IdentifyBuffer builds a 512 byte IDENTIFY DEVICE answer.
func IdentifyBuffer(s IdentifySpec) []byte {
	buf := make([]byte, 512)
	putATAString(buf, 27, 46, s.Model)
	putATAString(buf, 10, 19, s.Serial)
	putATAString(buf, 23, 26, s.Firmware)
	putWord(buf, 83, 1<<14|1<<10)
	binary.LittleEndian.PutUint64(buf[100*2:], s.Sectors)
	putWord(buf, 217, s.Rotation)
	if s.WWN != 0 {
		putWord(buf, 87, 1<<14|1<<8)
		putWord(buf, 108, uint16(s.WWN>>48))
		putWord(buf, 109, uint16(s.WWN>>32))
		putWord(buf, 110, uint16(s.WWN>>16))
		putWord(buf, 111, uint16(s.WWN))
	}
	if s.SMARTSupported {
		putWord(buf, 82, 1)
	}
	if s.SMARTEnabled {
		putWord(buf, 85, 1)
	}
	return buf
}

// RawAttr is one entry of a SMART table.
type RawAttr struct {
	ID                      uint8
	Value, Worst, Threshold uint8
	Raw                     [6]byte
}

// RawNumber splits a number into the six raw bytes of an attribute, least significant first.
func RawNumber(n uint64) [6]byte {
	var out [6]byte
	for i := range out {
		out[i] = byte(n >> (8 * i))
	}
	return out
}

// SmartDataBuffer builds the 512 byte SMART READ DATA answer.
func SmartDataBuffer(attrs []RawAttr) []byte {
	buf := make([]byte, 512)
	for i, a := range attrs {
		off := 2 + 12*i
		buf[off] = a.ID
		buf[off+3] = a.Value
		buf[off+4] = a.Worst
		copy(buf[off+5:off+11], a.Raw[:])
	}
	return buf
}

// ThresholdBuffer builds the 512 byte SMART READ THRESHOLDS answer.
func ThresholdBuffer(attrs []RawAttr) []byte {
	buf := make([]byte, 512)
	for i, a := range attrs {
		off := 2 + 12*i
		buf[off] = a.ID
		buf[off+1] = a.Threshold
	}
	return buf
}

// NVMeSmartSpec describes the SMART/Health log page for NVMeSmartBuffer.
type NVMeSmartSpec struct {
	CriticalWarning byte
	CompositeKelvin uint16
	AvailableSpare  byte
	SpareThreshold  byte
	PercentageUsed  byte
	UnitsRead       uint64
	UnitsWritten    uint64
	PowerCycles     uint64
	PowerOnHours    uint64
	UnsafeShutdowns uint64
	MediaErrors     uint64
	SensorsKelvin   [8]uint16
}

// NVMeSmartBuffer builds a 512 byte SMART/Health log page.
func NVMeSmartBuffer(s NVMeSmartSpec) []byte {
	buf := make([]byte, 512)
	buf[0] = s.CriticalWarning
	binary.LittleEndian.PutUint16(buf[1:], s.CompositeKelvin)
	buf[3] = s.AvailableSpare
	buf[4] = s.SpareThreshold
	buf[5] = s.PercentageUsed
	binary.LittleEndian.PutUint64(buf[32:], s.UnitsRead)
	binary.LittleEndian.PutUint64(buf[48:], s.UnitsWritten)
	binary.LittleEndian.PutUint64(buf[112:], s.PowerCycles)
	binary.LittleEndian.PutUint64(buf[128:], s.PowerOnHours)
	binary.LittleEndian.PutUint64(buf[144:], s.UnsafeShutdowns)
	binary.LittleEndian.PutUint64(buf[160:], s.MediaErrors)
	for i, k := range s.SensorsKelvin {
		binary.LittleEndian.PutUint16(buf[200+2*i:], k)
	}
	return buf
}

// NVMeIdentifyBuffer builds a 4096 byte Identify Controller answer.
func NVMeIdentifyBuffer(model, serial, firmware string, capacity uint64) []byte {
	buf := make([]byte, 4096)
	pad := func(dst []byte, s string) {
		for i := range dst {
			dst[i] = ' '
		}
		copy(dst, s)
	}
	pad(buf[4:24], serial)
	pad(buf[24:64], model)
	pad(buf[64:72], firmware)
	binary.LittleEndian.PutUint64(buf[280:], capacity)
	return buf
}

// ATA is a fake SATA transport: it answers from canned buffers, records every command and refuses what
// the allow list refuses, as the real transports do.
type ATA struct {
	Identify, Data, Thresholds, StatsPage []byte
	Status                                ATAStatus
	PowerMode                             uint8
	NoRegisters                           bool
	FailWith                              error
	Sent                                  []smartdecode.ATACommand
}

// ATAStatus holds the LBA mid and high registers of SMART RETURN STATUS.
type ATAStatus struct{ LBAMid, LBAHigh uint8 }

// ATA implements smartdecode.ATATransport.
func (f *ATA) ATA(cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	f.Sent = append(f.Sent, cmd)
	if err := smartdecode.CheckATA(cmd); err != nil {
		return smartdecode.ATAResult{}, err
	}
	if f.FailWith != nil {
		return smartdecode.ATAResult{}, f.FailWith
	}
	regs := !f.NoRegisters
	switch {
	case cmd.Command == 0xEC:
		return smartdecode.ATAResult{Data: f.Identify}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xD0:
		return smartdecode.ATAResult{Data: f.Data}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xD1:
		return smartdecode.ATAResult{Data: f.Thresholds}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xDA:
		return smartdecode.ATAResult{Registers: smartdecode.ATARegisters{LBAMid: f.Status.LBAMid, LBAHigh: f.Status.LBAHigh}, HaveRegisters: regs}, nil
	case cmd.Command == 0xE5:
		return smartdecode.ATAResult{Registers: smartdecode.ATARegisters{SectorCount: f.PowerMode}, HaveRegisters: regs}, nil
	case cmd.Command == 0x2F:
		return smartdecode.ATAResult{Data: f.StatsPage}, nil
	}
	return smartdecode.ATAResult{}, errors.New("unexpected command")
}

// Commands lists what was sent: the command byte, or the feature byte for a SMART command.
func (f *ATA) Commands() []uint8 {
	var out []uint8
	for _, c := range f.Sent {
		if c.Command == 0xB0 {
			out = append(out, c.Features)
		} else {
			out = append(out, c.Command)
		}
	}
	return out
}

// NVMe is a fake NVMe transport.
type NVMe struct {
	Identify, Log []byte
	FailWith      error
	Sent          []smartdecode.NVMeCommand
}

// Admin implements smartdecode.NVMeTransport.
func (f *NVMe) Admin(cmd smartdecode.NVMeCommand) ([]byte, error) {
	f.Sent = append(f.Sent, cmd)
	if err := smartdecode.CheckNVMe(cmd); err != nil {
		return nil, err
	}
	if f.FailWith != nil {
		return nil, f.FailWith
	}
	if cmd.Opcode == 0x06 {
		return f.Identify, nil
	}
	return f.Log, nil
}

// HealthyATA returns a fake SSD that answers every command and passes SMART.
func HealthyATA(serial string) *ATA {
	attrs := []RawAttr{
		{ID: 5, Value: 100, Worst: 100, Threshold: 10},
		{ID: 9, Value: 98, Worst: 98, Raw: RawNumber(100)},
		{ID: 12, Value: 99, Worst: 99, Raw: RawNumber(12)},
		{ID: 194, Value: 60, Worst: 50, Raw: [6]byte{38}},
		{ID: 231, Value: 93, Worst: 93, Threshold: 10},
	}
	return &ATA{
		Identify: IdentifyBuffer(IdentifySpec{
			Model: "ACME SSD", Serial: serial, Firmware: "F1", Sectors: 1000, Rotation: 1,
			SMARTSupported: true, SMARTEnabled: true,
		}),
		Data:       SmartDataBuffer(attrs),
		Thresholds: ThresholdBuffer(attrs),
		Status:     ATAStatus{LBAMid: 0x4F, LBAHigh: 0xC2},
		PowerMode:  0xFF,
	}
}

// HealthyNVMe returns a fake NVMe controller with a composite temperature of 45 C and 7 percent used.
func HealthyNVMe(serial string) *NVMe {
	return &NVMe{
		Identify: NVMeIdentifyBuffer("ACME NVMe", serial, "FW9", 1000204886016),
		Log: NVMeSmartBuffer(NVMeSmartSpec{
			CompositeKelvin: 318, PercentageUsed: 7, AvailableSpare: 100, SpareThreshold: 10,
			PowerOnHours: 100, PowerCycles: 12, UnitsRead: 20, UnitsWritten: 10, MediaErrors: 3,
			SensorsKelvin: [8]uint16{320, 305},
		}),
	}
}
