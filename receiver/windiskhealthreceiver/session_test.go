// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

// fakeController answers each call with the next canned reply and records every request it receives.
type fakeController struct {
	calls   []request
	replies [][]byte
	err     error
	closed  bool
}

func (f *fakeController) control(code uint32, in []byte, outLen int) ([]byte, error) {
	f.calls = append(f.calls, request{code: code, in: in, outLen: outLen})
	if f.err != nil {
		return nil, f.err
	}
	if len(f.replies) == 0 {
		return make([]byte, outLen), nil
	}
	out := f.replies[0]
	f.replies = f.replies[1:]
	return out, nil
}

func (f *fakeController) Close() error { f.closed = true; return nil }

func newSession(c *fakeController) *diskSession {
	return &diskSession{c: c, timeout: 4500 * time.Millisecond}
}

func TestSession_ARefusedRequestIsNotSent(t *testing.T) {
	c := &fakeController{}
	s := newSession(c)

	_, err := s.send(request{code: 0x0007C084, in: make([]byte, 12), outLen: 64})
	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed)
	_, err = s.ATA(smartdecode.ATACommand{Command: 0xB0, Features: 0xD8})
	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed)
	_, err = s.Admin(smartdecode.NVMeCommand{Opcode: 0x09, DataLen: 16})
	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed)
	assert.Empty(t, c.calls, "nothing reached the controller")
}

func TestSession_ATASendsPassThroughAndDecodesTheAnswer(t *testing.T) {
	reply := make([]byte, ataHeaderLen+512)
	reply[ataHeaderLen] = 0x77
	c := &fakeController{replies: [][]byte{reply}}
	res, err := newSession(c).ATA(smartdecode.IdentifyCommand())
	require.NoError(t, err)
	assert.Equal(t, byte(0x77), res.Data[0])
	require.Len(t, c.calls, 1)
	assert.Equal(t, ioctlATAPassThrough, c.calls[0].code)
	assert.Equal(t, uint32(5), littleEndian.Uint32(c.calls[0].in[12:]), "4.5 s rounds up to 5 s for the driver")
	assert.Equal(t, byte(0xEC), c.calls[0].in[ataTaskFile+6])
}

func TestSession_AdminSendsTheProtocolQuery(t *testing.T) {
	log := make([]byte, protocolOutHeader+nvmeHealthBytes)
	log[protocolOutHeader+1] = 9
	ident := make([]byte, protocolOutHeader+nvmeIdentifyBytes)
	c := &fakeController{replies: [][]byte{ident, log}}
	s := newSession(c)

	page, err := s.Admin(smartdecode.IdentifyControllerCommand())
	require.NoError(t, err)
	assert.Len(t, page, nvmeIdentifyBytes)
	page, err = s.Admin(smartdecode.SmartLogCommand())
	require.NoError(t, err)
	assert.Equal(t, byte(9), page[1])

	require.Len(t, c.calls, 2)
	assert.Equal(t, nvmeIdentifyRequest().in, c.calls[0].in)
	assert.Equal(t, nvmeHealthRequest().in, c.calls[1].in)
	q := c.calls[1].in
	assert.Equal(t, propProtocolSpecific, littleEndian.Uint32(q))
	assert.Equal(t, uint32(protocolTypeNVMe), littleEndian.Uint32(q[8:]))
	assert.Equal(t, uint32(nvmeDataLogPage), littleEndian.Uint32(q[12:]))
	assert.Equal(t, uint32(nvmeLogHealthInfo), littleEndian.Uint32(q[16:]))
	assert.Equal(t, uint32(protocolDataLen), littleEndian.Uint32(q[24:]), "the page follows the specific data")
	assert.Equal(t, uint32(nvmeHealthBytes), littleEndian.Uint32(q[28:]))
}

func TestSession_TemperatureFailurePredictionAndDescriptor(t *testing.T) {
	temp := make([]byte, temperatureHeader+temperatureInfoLen)
	littleEndian.PutUint16(temp[12:], 1)
	littleEndian.PutUint16(temp[temperatureHeader+2:], 36)
	predict := make([]byte, predictFailureLen)
	littleEndian.PutUint32(predict, 1)
	c := &fakeController{replies: [][]byte{temp, predict, descriptorBuffer(busNVMe, "", "ACME", "", "S1")}}
	s := newSession(c)

	got, err := s.temperature()
	require.NoError(t, err)
	assert.Equal(t, 36, got)
	failing, err := s.predictFailure()
	require.NoError(t, err)
	assert.True(t, failing)
	d, err := s.descriptor()
	require.NoError(t, err)
	assert.Equal(t, "S1", d.serial)

	require.Len(t, c.calls, 3)
	assert.Equal(t, ioctlStorageQueryProperty, c.calls[0].code)
	assert.Equal(t, propTemperature, littleEndian.Uint32(c.calls[0].in))
	assert.Equal(t, ioctlStoragePredictFailure, c.calls[1].code)
	assert.Equal(t, propDevice, littleEndian.Uint32(c.calls[2].in))
}

func TestSession_AControllerErrorKeepsItsMeaning(t *testing.T) {
	c := &fakeController{err: mapWin32(win32AccessDenied, errors.New("Access is denied."))}
	s := newSession(c)
	_, err := s.temperature()
	assert.ErrorIs(t, err, smartdecode.ErrPermission)
	_, err = s.predictFailure()
	assert.ErrorIs(t, err, smartdecode.ErrPermission)
	_, err = s.descriptor()
	assert.ErrorIs(t, err, smartdecode.ErrPermission)
	_, err = s.ATA(smartdecode.IdentifyCommand())
	assert.ErrorIs(t, err, smartdecode.ErrPermission)
	_, err = s.Admin(smartdecode.SmartLogCommand())
	assert.ErrorIs(t, err, smartdecode.ErrPermission)
}

// driver stands in for the Windows storage stack: it decodes a request and answers from a smartdecode fake.
type driver struct {
	ata  *smarttest.ATA
	nvme *smarttest.NVMe
}

func (d *driver) Close() error { return nil }

func (d *driver) control(code uint32, in []byte, _ int) ([]byte, error) {
	if code == ioctlATAPassThrough {
		tf := in[ataTaskFile:]
		cmd := smartdecode.ATACommand{
			Features: tf[0], SectorCount: tf[1], LBALow: tf[2], LBAMid: tf[3], LBAHigh: tf[4],
			Device: tf[5] &^ ataDeviceLBA, Command: tf[6], DataLen: int(littleEndian.Uint32(in[8:])),
		}
		if littleEndian.Uint16(in[2:])&ataFlagDataIn != 0 {
			cmd.Protocol = smartdecode.ProtoPIOIn
		} else {
			cmd.WantRegisters = true
		}
		res, err := d.ata.ATA(cmd)
		if err != nil {
			return nil, err
		}
		out := append([]byte(nil), in...)
		copy(out[ataHeaderLen:], res.Data)
		r := res.Registers
		copy(out[ataTaskFile:], []byte{r.Error, r.SectorCount, r.LBALow, r.LBAMid, r.LBAHigh, r.Device, r.Status})
		return out, nil
	}
	cmd := smartdecode.SmartLogCommand()
	if littleEndian.Uint32(in[queryHeaderLen+4:]) == nvmeDataIdentify {
		cmd = smartdecode.IdentifyControllerCommand()
	}
	page, err := d.nvme.Admin(cmd)
	if err != nil {
		return nil, err
	}
	return append(make([]byte, protocolOutHeader), page...), nil
}

func TestSession_TheSmartDecodersReadATAThroughThePassThroughEncoding(t *testing.T) {
	fake := smarttest.HealthyATA("SN9")
	data, err := smartdecode.ReadATA(newSession(&fakeController{}).over(&driver{ata: fake}), smartdecode.ATAOptions{SkipIfStandby: true})
	require.NoError(t, err)
	assert.Equal(t, "SN9", data.Identify.Serial)
	assert.Equal(t, smartdecode.HealthOK, data.Health)
	assert.Equal(t, []uint8{0xE5, 0xEC, 0xD0, 0xD1, 0xDA}, fake.Commands())

	failing := smarttest.HealthyATA("SN9")
	failing.Status = smarttest.ATAStatus{LBAMid: 0xF4, LBAHigh: 0x2C}
	data, err = smartdecode.ReadATA(newSession(&fakeController{}).over(&driver{ata: failing}), smartdecode.ATAOptions{})
	require.NoError(t, err)
	assert.Equal(t, smartdecode.HealthPredictedFailure, data.Health)
}

func TestSession_TheSmartDecodersReadNVMeThroughTheProtocolQuery(t *testing.T) {
	data, err := smartdecode.ReadNVMe(newSession(&fakeController{}).over(&driver{nvme: smarttest.HealthyNVMe("NV9")}))
	require.NoError(t, err)
	assert.Equal(t, "NV9", data.Identify.Serial)
	assert.Equal(t, uint64(3), data.Smart.MediaErrors)
	assert.True(t, data.Smart.HasComposite)
}

// over gives a session on another controller; the timeout is kept.
func (s *diskSession) over(c controller) *diskSession { return &diskSession{c: c, timeout: s.timeout} }
