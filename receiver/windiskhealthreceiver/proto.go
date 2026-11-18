// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// Control codes the receiver may send. Every one is a read request.
const (
	ioctlStorageQueryProperty  uint32 = 0x002D1400
	ioctlStoragePredictFailure uint32 = 0x002D1100
	ioctlATAPassThrough        uint32 = 0x0004D02C
)

// Storage property ids of IOCTL_STORAGE_QUERY_PROPERTY.
const (
	propDevice           uint32 = 0
	propProtocolSpecific uint32 = 50
	propTemperature      uint32 = 52
)

const (
	queryLen           = 12 // STORAGE_PROPERTY_QUERY with one byte of additional parameters, padded
	queryHeaderLen     = 8
	protocolDataLen    = 40 // STORAGE_PROTOCOL_SPECIFIC_DATA
	protocolTypeNVMe   = 3
	nvmeDataIdentify   = 1
	nvmeDataLogPage    = 2
	nvmeCNSController  = 1
	nvmeLogHealthInfo  = 2
	nvmeIdentifyBytes  = 4096
	nvmeHealthBytes    = 512
	protocolOutHeader  = 8 + protocolDataLen // descriptor header, then the specific data, then the page
	deviceOutLen       = 1024
	temperatureOutLen  = 64
	predictFailureLen  = 516
	temperatureHeader  = 24
	temperatureInfoLen = 16
)

// ATA_PASS_THROUGH_EX on a 64-bit system, with the data after the header in the same buffer.
const (
	ataHeaderLen  = 48
	ataTaskFile   = 40 // CurrentTaskFile: features, count, lba low, mid, high, device, command
	ataFlagDRDY   = 0x01
	ataFlagDataIn = 0x02
	ataFlag48Bit  = 0x08
	ataDeviceLBA  = 0x40
	ataStatusErr  = 0x01

	// maxATATimeoutSec bounds the driver timeout of a request; the receiver asks for the command timeout.
	maxATATimeoutSec = 3600
)

// Storage bus types of STORAGE_BUS_TYPE that the receiver tells apart.
const (
	busNVMe    uint32 = 17
	busRAID    uint32 = 8
	busVirtual uint32 = 14
	busFileVM  uint32 = 15
	busSpaces  uint32 = 16
)

var littleEndian = binary.LittleEndian

// request is the argument set of one DeviceIoControl call.
type request struct {
	code   uint32
	in     []byte
	outLen int
}

func propertyQuery(id uint32) []byte {
	b := make([]byte, queryLen)
	littleEndian.PutUint32(b, id)
	return b
}

func nvmeQuery(dataType, value uint32, dataLen int) []byte {
	b := make([]byte, queryHeaderLen+protocolDataLen)
	littleEndian.PutUint32(b, propProtocolSpecific)
	p := b[queryHeaderLen:]
	littleEndian.PutUint32(p[0:], protocolTypeNVMe)
	littleEndian.PutUint32(p[4:], dataType)
	littleEndian.PutUint32(p[8:], value)
	littleEndian.PutUint32(p[16:], protocolDataLen)
	littleEndian.PutUint32(p[20:], uint32(dataLen))
	return b
}

func deviceRequest() request {
	return request{code: ioctlStorageQueryProperty, in: propertyQuery(propDevice), outLen: deviceOutLen}
}

func temperatureRequest() request {
	return request{code: ioctlStorageQueryProperty, in: propertyQuery(propTemperature), outLen: temperatureOutLen}
}

func predictFailureRequest() request {
	return request{code: ioctlStoragePredictFailure, outLen: predictFailureLen}
}

func nvmeIdentifyRequest() request {
	return request{code: ioctlStorageQueryProperty, in: nvmeQuery(nvmeDataIdentify, nvmeCNSController, nvmeIdentifyBytes), outLen: protocolOutHeader + nvmeIdentifyBytes}
}

func nvmeHealthRequest() request {
	return request{code: ioctlStorageQueryProperty, in: nvmeQuery(nvmeDataLogPage, nvmeLogHealthInfo, nvmeHealthBytes), outLen: protocolOutHeader + nvmeHealthBytes}
}

// ataRequest wraps one allowed ATA command in ATA_PASS_THROUGH_EX; timeoutSec is the driver's own timeout.
func ataRequest(cmd smartdecode.ATACommand, timeoutSec uint32) request {
	b := make([]byte, ataHeaderLen+cmd.DataLen)
	flags := uint16(ataFlagDRDY)
	if cmd.Protocol == smartdecode.ProtoPIOIn {
		flags |= ataFlagDataIn
	}
	if cmd.Is48Bit() {
		flags |= ataFlag48Bit
	}
	littleEndian.PutUint16(b[0:], ataHeaderLen)
	littleEndian.PutUint16(b[2:], flags)
	littleEndian.PutUint32(b[8:], uint32(cmd.DataLen))
	littleEndian.PutUint32(b[12:], timeoutSec)
	littleEndian.PutUint64(b[24:], ataHeaderLen)
	copy(b[ataTaskFile:], []byte{cmd.Features, cmd.SectorCount, cmd.LBALow, cmd.LBAMid, cmd.LBAHigh, cmd.Device | ataDeviceLBA, cmd.Command})
	return request{code: ioctlATAPassThrough, in: b, outLen: len(b)}
}

// checkRequest is the one place that decides what may be sent. A request passes only when it equals one of
// the property and failure queries byte for byte, or is an ATA pass-through of a command on the smartdecode
// list, built for reading.
func checkRequest(r request) error {
	switch r.code {
	case ioctlStorageQueryProperty, ioctlStoragePredictFailure:
		for _, allowed := range []request{deviceRequest(), temperatureRequest(), predictFailureRequest(), nvmeIdentifyRequest(), nvmeHealthRequest()} {
			if r.code == allowed.code && r.outLen == allowed.outLen && bytes.Equal(r.in, allowed.in) {
				return nil
			}
		}
	case ioctlATAPassThrough:
		return checkATARequest(r)
	}
	return fmt.Errorf("%w: control code %#08x", smartdecode.ErrCommandNotAllowed, r.code)
}

func checkATARequest(r request) error {
	in := r.in
	refuse := func(why string) error {
		return fmt.Errorf("%w: ATA pass-through %s", smartdecode.ErrCommandNotAllowed, why)
	}
	if len(in) < ataHeaderLen || r.outLen != len(in) {
		return refuse("buffer size")
	}
	flags := littleEndian.Uint16(in[2:])
	dataLen := littleEndian.Uint32(in[8:])
	switch {
	case littleEndian.Uint16(in[0:]) != ataHeaderLen, littleEndian.Uint64(in[24:]) != ataHeaderLen:
		return refuse("header")
	case flags&^(ataFlagDRDY|ataFlagDataIn|ataFlag48Bit) != 0:
		return refuse("flags")
	case in[4] != 0 || in[5] != 0 || in[6] != 0:
		return refuse("address")
	case int(dataLen) != len(in)-ataHeaderLen:
		return refuse("data length")
	}
	tf := in[ataTaskFile:]
	cmd := smartdecode.ATACommand{
		Features: tf[0], SectorCount: tf[1], LBALow: tf[2], LBAMid: tf[3], LBAHigh: tf[4],
		Device: tf[5] &^ ataDeviceLBA, Command: tf[6], DataLen: int(dataLen),
	}
	if flags&ataFlagDataIn != 0 {
		cmd.Protocol = smartdecode.ProtoPIOIn
	} else {
		cmd.WantRegisters = true
	}
	if (flags&ataFlag48Bit != 0) != cmd.Is48Bit() {
		return refuse("48-bit flag")
	}
	if err := smartdecode.CheckATA(cmd); err != nil {
		return err
	}
	timeout := littleEndian.Uint32(in[12:])
	if timeout == 0 || timeout > maxATATimeoutSec {
		return refuse("timeout")
	}
	// Every byte, the reserved ones and the previous task file included, must be what ataRequest builds.
	if !bytes.Equal(in, ataRequest(cmd, timeout).in) {
		return refuse("bytes outside the built request")
	}
	return nil
}

// decodeATA turns the output of ATA_PASS_THROUGH_EX into the transport answer.
func decodeATA(out []byte, cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	if len(out) < ataHeaderLen+cmd.DataLen {
		return smartdecode.ATAResult{}, fmt.Errorf("%w: ATA answer of %d bytes", smartdecode.ErrShortBuffer, len(out))
	}
	tf := out[ataTaskFile:]
	res := smartdecode.ATAResult{Data: out[ataHeaderLen : ataHeaderLen+cmd.DataLen]}
	if cmd.WantRegisters {
		res.HaveRegisters = true
		res.Registers = smartdecode.ATARegisters{Error: tf[0], SectorCount: tf[1], LBALow: tf[2], LBAMid: tf[3], LBAHigh: tf[4], Device: tf[5], Status: tf[6]}
		return res, nil
	}
	if tf[6]&ataStatusErr != 0 {
		return smartdecode.ATAResult{}, fmt.Errorf("%w: the device aborted command %#02x", smartdecode.ErrUnsupported, cmd.Command)
	}
	return res, nil
}

// decodeProtocolData returns the page of a protocol-specific query answer.
func decodeProtocolData(out []byte, want int) ([]byte, error) {
	if len(out) < protocolOutHeader+want {
		return nil, fmt.Errorf("%w: protocol answer of %d bytes", smartdecode.ErrShortBuffer, len(out))
	}
	return out[protocolOutHeader : protocolOutHeader+want], nil
}

// Field offsets of the Windows storage structures read below.
const (
	temperatureSensorCountAt = 12 // STORAGE_TEMPERATURE_DATA_DESCRIPTOR.InfoCount
	temperatureValueAt       = 2  // STORAGE_TEMPERATURE_INFO.Temperature, after Index
	deviceBusTypeAt          = 28 // STORAGE_DEVICE_DESCRIPTOR.BusType
	deviceVendorAt           = 12
	deviceProductAt          = 16
	deviceFirmwareAt         = 20
	deviceSerialAt           = 24
)

// temperatureNotReported is STORAGE_TEMPERATURE_VALUE_NOT_REPORTED: the driver has no value.
const temperatureNotReported = 0x8000

// decodeTemperature reads the first sensor of STORAGE_TEMPERATURE_DATA_DESCRIPTOR, in degrees Celsius.
func decodeTemperature(out []byte) (int, error) {
	if len(out) < temperatureHeader+temperatureInfoLen || littleEndian.Uint16(out[temperatureSensorCountAt:]) == 0 {
		return 0, fmt.Errorf("%w: no temperature sensor in the answer", smartdecode.ErrUnsupported)
	}
	raw := littleEndian.Uint16(out[temperatureHeader+temperatureValueAt:])
	if raw == temperatureNotReported {
		return 0, fmt.Errorf("%w: the driver does not report a temperature", smartdecode.ErrUnsupported)
	}
	return int(int16(raw)), nil
}

// decodePredictFailure reads STORAGE_PREDICT_FAILURE; a non-zero value means the drive predicts failure.
func decodePredictFailure(out []byte) (bool, error) {
	if len(out) < 4 {
		return false, fmt.Errorf("%w: failure prediction answer of %d bytes", smartdecode.ErrShortBuffer, len(out))
	}
	return littleEndian.Uint32(out) != 0, nil
}

// descriptor is what STORAGE_DEVICE_DESCRIPTOR says about a disk.
type descriptor struct {
	busType                           uint32
	vendor, product, firmware, serial string
}

func decodeDescriptor(out []byte) (descriptor, error) {
	if len(out) < 36 {
		return descriptor{}, fmt.Errorf("%w: device descriptor of %d bytes", smartdecode.ErrShortBuffer, len(out))
	}
	d := descriptor{busType: littleEndian.Uint32(out[deviceBusTypeAt:])}
	for _, field := range []struct {
		at  int
		dst *string
	}{{deviceVendorAt, &d.vendor}, {deviceProductAt, &d.product}, {deviceFirmwareAt, &d.firmware}, {deviceSerialAt, &d.serial}} {
		s, err := cString(out, littleEndian.Uint32(out[field.at:]))
		if err != nil {
			return descriptor{}, err
		}
		*field.dst = s
	}
	return d, nil
}

// noString is the offset some drivers give for a field they do not have, besides 0.
const noString = 0xFFFFFFFF

// cString reads the NUL-terminated string at an offset of the buffer; offset 0 and all ones mean absent.
func cString(buf []byte, off uint32) (string, error) {
	if off == 0 || off == noString {
		return "", nil
	}
	if int(off) >= len(buf) {
		return "", fmt.Errorf("%w: string offset %d in a buffer of %d bytes", smartdecode.ErrShortBuffer, off, len(buf))
	}
	s := buf[off:]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(string(s)), nil
}

// diskKind says how the receiver reads a disk.
type diskKind int

const (
	kindATA diskKind = iota
	kindNVMe
	kindSkip // RAID virtual drive or virtual disk: the controller hides the physical disk
)

func (d descriptor) kind() diskKind {
	switch d.busType {
	case busNVMe:
		return kindNVMe
	case busRAID, busVirtual, busFileVM, busSpaces:
		return kindSkip
	}
	return kindATA
}
