// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func TestCheckRequest_TheAllowedRequestsPass(t *testing.T) {
	allowed := map[string]request{
		"device descriptor":   deviceRequest(),
		"temperature":         temperatureRequest(),
		"failure prediction":  predictFailureRequest(),
		"NVMe identify":       nvmeIdentifyRequest(),
		"NVMe health log":     nvmeHealthRequest(),
		"ATA identify":        ataRequest(smartdecode.IdentifyCommand(), 5),
		"ATA smart data":      ataRequest(smartdecode.SmartReadDataCommand(), 5),
		"ATA smart threshold": ataRequest(smartdecode.SmartReadThresholdsCommand(), 5),
		"ATA smart status":    ataRequest(smartdecode.SmartReturnStatusCommand(), 5),
		"ATA power mode":      ataRequest(smartdecode.CheckPowerModeCommand(), 5),
		"ATA statistics":      ataRequest(smartdecode.DeviceStatisticsCommand(5), 5),
	}
	for name, r := range allowed {
		assert.NoError(t, checkRequest(r), name)
	}
}

func TestCheckRequest_EveryOtherControlCodeIsRefused(t *testing.T) {
	codes := map[string]uint32{
		"disk get drive geometry":  0x00070000,
		"smart send drive command": 0x0007C084,
		"smart receive drive data": 0x0007C088,
		"ata pass through direct":  0x0004D030,
		"scsi pass through":        0x0004D004,
		"disk set drive layout":    0x0007C054,
		"storage reset device":     0x002D5004,
		"storage manage data set":  0x002D9404,
		"zero":                     0,
		"query property all ones":  0xFFFFFFFF,
		"storage_check_verify":     0x002D0800,
	}
	for name, code := range codes {
		err := checkRequest(request{code: code, in: propertyQuery(propDevice), outLen: deviceOutLen})
		require.Error(t, err, name)
		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, name)
	}
}

func TestCheckRequest_AQueryOfAnotherPropertyOrShapeIsRefused(t *testing.T) {
	other := func(mutate func(in []byte) []byte, base request) request {
		in := append([]byte(nil), base.in...)
		return request{code: base.code, in: mutate(in), outLen: base.outLen}
	}
	cases := map[string]request{
		"adapter temperature property": other(func(in []byte) []byte { littleEndian.PutUint32(in, 51); return in }, temperatureRequest()),
		"seek penalty property":        other(func(in []byte) []byte { littleEndian.PutUint32(in, 7); return in }, deviceRequest()),
		"exists query":                 other(func(in []byte) []byte { littleEndian.PutUint32(in[4:], 1); return in }, deviceRequest()),
		"mask query":                   other(func(in []byte) []byte { littleEndian.PutUint32(in[4:], 2); return in }, deviceRequest()),
		"longer input":                 other(func(in []byte) []byte { return append(in, 0) }, deviceRequest()),
		"short input":                  other(func(in []byte) []byte { return in[:8] }, deviceRequest()),
		"larger output":                {code: ioctlStorageQueryProperty, in: propertyQuery(propDevice), outLen: deviceOutLen + 1},
		"NVMe namespace identify":      other(func(in []byte) []byte { littleEndian.PutUint32(in[queryHeaderLen+8:], 0); return in }, nvmeIdentifyRequest()),
		"NVMe other log page":          other(func(in []byte) []byte { littleEndian.PutUint32(in[queryHeaderLen+8:], 3); return in }, nvmeHealthRequest()),
		"NVMe set features data type":  other(func(in []byte) []byte { littleEndian.PutUint32(in[queryHeaderLen+4:], 3); return in }, nvmeHealthRequest()),
		"NVMe other protocol":          other(func(in []byte) []byte { littleEndian.PutUint32(in[queryHeaderLen:], 1); return in }, nvmeHealthRequest()),
		"failure prediction with data": {code: ioctlStoragePredictFailure, in: []byte{1}, outLen: predictFailureLen},
	}
	for name, r := range cases {
		err := checkRequest(r)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, name)
	}
}

func TestCheckRequest_AnATARequestOutsideTheReadOnlyListIsRefused(t *testing.T) {
	build := func(command, features uint8) request {
		return ataRequest(smartdecode.ATACommand{Command: command, Features: features, LBAMid: 0x4F, LBAHigh: 0xC2, Protocol: smartdecode.ProtoNonData, WantRegisters: true}, 5)
	}
	cases := map[string]request{
		"smart enable operations": build(0xB0, 0xD8),
		"smart disable":           build(0xB0, 0xD9),
		"smart autosave":          build(0xB0, 0xD2),
		"smart execute self test": build(0xB0, 0xD4),
		"set features":            build(0xEF, 0x02),
		"security erase":          build(0xF4, 0),
		"write dma":               build(0xCA, 0),
		"standby immediate":       build(0xE0, 0),
	}
	data := ataRequest(smartdecode.IdentifyCommand(), 5)
	out := append([]byte(nil), data.in...)
	littleEndian.PutUint16(out[2:], ataFlagDRDY|ataFlagDataIn|0x04)
	cases["data out flag"] = request{code: data.code, in: out, outLen: data.outLen}
	out = append([]byte(nil), data.in...)
	littleEndian.PutUint32(out[8:], 0)
	cases["data length that does not fit the buffer"] = request{code: data.code, in: out, outLen: data.outLen}
	out = append([]byte(nil), data.in...)
	littleEndian.PutUint64(out[24:], 0)
	cases["data offset"] = request{code: data.code, in: out, outLen: data.outLen}
	out = append([]byte(nil), data.in...)
	out[5] = 1
	cases["target address"] = request{code: data.code, in: out, outLen: data.outLen}
	cases["short buffer"] = request{code: ioctlATAPassThrough, in: out[:10], outLen: 10}
	out = append([]byte(nil), ataRequest(smartdecode.DeviceStatisticsCommand(5), 5).in...)
	littleEndian.PutUint16(out[2:], ataFlagDRDY|ataFlagDataIn)
	cases["48-bit command without the flag"] = request{code: ioctlATAPassThrough, in: out, outLen: len(out)}

	for name, r := range cases {
		err := checkRequest(r)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, name)
	}
}

func TestATARequest_LayoutOfTheHeaderAndTheTaskFile(t *testing.T) {
	r := ataRequest(smartdecode.SmartReadDataCommand(), 7)
	require.Len(t, r.in, ataHeaderLen+512)
	assert.Equal(t, ioctlATAPassThrough, r.code)
	assert.Equal(t, uint16(ataHeaderLen), littleEndian.Uint16(r.in[0:]))
	assert.Equal(t, uint16(ataFlagDRDY|ataFlagDataIn), littleEndian.Uint16(r.in[2:]))
	assert.Equal(t, uint32(512), littleEndian.Uint32(r.in[8:]))
	assert.Equal(t, uint32(7), littleEndian.Uint32(r.in[12:]))
	assert.Equal(t, uint64(ataHeaderLen), littleEndian.Uint64(r.in[24:]))
	assert.Equal(t, []byte{0xD0, 1, 0, 0x4F, 0xC2, ataDeviceLBA, 0xB0}, r.in[ataTaskFile:ataTaskFile+7])

	status := ataRequest(smartdecode.SmartReturnStatusCommand(), 5)
	assert.Equal(t, uint16(ataFlagDRDY), littleEndian.Uint16(status.in[2:]), "no data: no data-in flag")
	log := ataRequest(smartdecode.DeviceStatisticsCommand(1), 5)
	assert.Equal(t, uint16(ataFlagDRDY|ataFlagDataIn|ataFlag48Bit), littleEndian.Uint16(log.in[2:]))
}

func TestDecodeATA_NoDataCommand_ReturnsTheRegisters(t *testing.T) {
	out := make([]byte, ataHeaderLen)
	copy(out[ataTaskFile:], []byte{0, 1, 2, 0x4F, 0xC2, 0xA0, 0x50})
	res, err := decodeATA(out, smartdecode.SmartReturnStatusCommand())
	require.NoError(t, err)
	assert.True(t, res.HaveRegisters)
	assert.Equal(t, smartdecode.ATARegisters{SectorCount: 1, LBALow: 2, LBAMid: 0x4F, LBAHigh: 0xC2, Device: 0xA0, Status: 0x50}, res.Registers)
}

func TestDecodeATA_DataCommand_ReturnsTheDataBlock(t *testing.T) {
	out := make([]byte, ataHeaderLen+512)
	out[ataHeaderLen] = 0x5A
	res, err := decodeATA(out, smartdecode.IdentifyCommand())
	require.NoError(t, err)
	assert.False(t, res.HaveRegisters)
	assert.Len(t, res.Data, 512)
	assert.Equal(t, byte(0x5A), res.Data[0])
}

func TestDecodeATA_ErrorStatus_ReturnsUnsupported(t *testing.T) {
	out := make([]byte, ataHeaderLen+512)
	out[ataTaskFile+6] = ataStatusErr
	_, err := decodeATA(out, smartdecode.IdentifyCommand())
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported, "an aborted command")
}

func TestDecodeATA_ShortAnswer_ReturnsShortBuffer(t *testing.T) {
	_, err := decodeATA(make([]byte, 20), smartdecode.IdentifyCommand())
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer)
}

func TestDecodeProtocolData(t *testing.T) {
	out := make([]byte, protocolOutHeader+nvmeHealthBytes)
	out[protocolOutHeader] = 7
	page, err := decodeProtocolData(out, nvmeHealthBytes)
	require.NoError(t, err)
	assert.Len(t, page, nvmeHealthBytes)
	assert.Equal(t, byte(7), page[0])
	_, err = decodeProtocolData(out[:100], nvmeHealthBytes)
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer)
}

func temperatureAnswer(sensors uint16, value uint16) []byte {
	out := make([]byte, temperatureHeader+temperatureInfoLen)
	littleEndian.PutUint16(out[12:], sensors)
	littleEndian.PutUint16(out[temperatureHeader+2:], value)
	return out
}

func TestDecodeTemperature_PositiveValue_ReturnsCelsius(t *testing.T) {
	got, err := decodeTemperature(temperatureAnswer(1, 41))
	require.NoError(t, err)
	assert.Equal(t, 41, got)
}

func TestDecodeTemperature_NegativeValue_ReturnsSignedCelsius(t *testing.T) {
	neg := int16(-5)
	got, err := decodeTemperature(temperatureAnswer(1, uint16(neg)))
	require.NoError(t, err)
	assert.Equal(t, -5, got)
}

func TestDecodeTemperature_NoSensor_ReturnsUnsupported(t *testing.T) {
	_, err := decodeTemperature(temperatureAnswer(0, 41))
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)
}

func TestDecodeTemperature_ShortAnswer_ReturnsUnsupported(t *testing.T) {
	_, err := decodeTemperature(temperatureAnswer(1, 41)[:10])
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)
}

func TestDecodePredictFailure_ZeroValue_ReturnsNotFailing(t *testing.T) {
	isFailing, err := decodePredictFailure(make([]byte, predictFailureLen))
	require.NoError(t, err)
	assert.False(t, isFailing)
}

func TestDecodePredictFailure_NonZeroValue_ReturnsFailing(t *testing.T) {
	out := make([]byte, predictFailureLen)
	littleEndian.PutUint32(out, 1)
	isFailing, err := decodePredictFailure(out)
	require.NoError(t, err)
	assert.True(t, isFailing)
}

func TestDecodePredictFailure_ShortAnswer_ReturnsShortBuffer(t *testing.T) {
	_, err := decodePredictFailure(make([]byte, 2))
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer)
}

func descriptorBuffer(bus uint32, vendor, product, firmware, serial string) []byte {
	buf := make([]byte, 36, 200)
	put := func(at int, s string) {
		if s == "" {
			return
		}
		littleEndian.PutUint32(buf[at:], uint32(len(buf)))
		buf = append(buf, s...)
		buf = append(buf, 0)
	}
	put(12, vendor)
	put(16, product)
	put(20, firmware)
	put(24, serial)
	littleEndian.PutUint32(buf[28:], bus)
	return buf
}

func TestDecodeDescriptor_AtaDisk_TrimsTheStringsAndKindIsATA(t *testing.T) {
	d, err := decodeDescriptor(descriptorBuffer(11, "ATA", "ACME SSD 1TB   ", "F1", " SN123 "))
	require.NoError(t, err)
	assert.Equal(t, descriptor{busType: 11, vendor: "ATA", product: "ACME SSD 1TB", firmware: "F1", serial: "SN123"}, d)
	assert.Equal(t, kindATA, d.kind())
}

func TestDecodeDescriptor_NVMeBus_KindIsNVMe(t *testing.T) {
	d, err := decodeDescriptor(descriptorBuffer(busNVMe, "", "NVMe disk", "", ""))
	require.NoError(t, err)
	assert.Equal(t, kindNVMe, d.kind())
	assert.Empty(t, d.serial)
}

func TestDescriptorKind_RaidAndVirtualBuses_AreSkipped(t *testing.T) {
	for _, bus := range []uint32{busRAID, busVirtual, busFileVM, busSpaces} {
		assert.Equal(t, kindSkip, descriptor{busType: bus}.kind(), "bus %d", bus)
	}
}

func TestDescriptorKind_OtherBuses_AreTriedAsATA(t *testing.T) {
	for _, bus := range []uint32{0, 1, 7, 10} {
		assert.Equal(t, kindATA, descriptor{busType: bus}.kind(), "bus %d", bus)
	}
}

func TestDecodeDescriptor_ShortAnswer_ReturnsShortBuffer(t *testing.T) {
	_, err := decodeDescriptor(make([]byte, 10))
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer)
}

func TestCString_AnOffsetPastTheBufferIsAShortBuffer(t *testing.T) {
	got, err := cString([]byte("abc"), 0)
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = cString([]byte("abc"), 1)
	require.NoError(t, err)
	assert.Equal(t, "bc", got)
	_, err = cString([]byte("abc"), 9)
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer)

	bad := descriptorBuffer(11, "", "ACME", "", "")
	littleEndian.PutUint32(bad[24:], 5000)
	_, err = decodeDescriptor(bad)
	assert.ErrorIs(t, err, smartdecode.ErrShortBuffer, "a truncated descriptor is an error, not an empty serial")
}

func TestMapWin32_KnownCodes_MapToTheSmartdecodeErrors(t *testing.T) {
	base := errors.New("win32")
	for code, want := range map[uint32]error{
		win32AccessDenied: smartdecode.ErrPermission, win32PrivilegeNotHeld: smartdecode.ErrPermission,
		win32SemTimeout: smartdecode.ErrTimeout, win32OperationAborted: smartdecode.ErrTimeout,
		win32InvalidFunction: smartdecode.ErrUnsupported, win32NotSupported: smartdecode.ErrUnsupported,
		win32InvalidParameter: smartdecode.ErrUnsupported, win32GeneralFailure: smartdecode.ErrUnsupported,
		win32IODevice: smartdecode.ErrUnsupported, win32DeviceNotConnected: smartdecode.ErrUnsupported,
	} {
		err := mapWin32(code, base)
		assert.ErrorIs(t, err, want, "code %d", code)
		assert.ErrorIs(t, err, base)
	}
}

func TestMapWin32_UnknownCode_PassesTheErrorThrough(t *testing.T) {
	base := errors.New("win32")
	assert.Equal(t, base, mapWin32(2, base))
}
