// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func TestStructLayoutsMatchTheKernelHeaders(t *testing.T) {
	assert.Equal(t, uintptr(72), unsafe.Sizeof(nvmeAdminCmd{}))
	assert.Equal(t, uintptr(0xC0484E41), uintptr(nvmeIoctlAdminCmd))
	assert.Equal(t, uintptr(0x2285), uintptr(sgIO))
	if unsafe.Sizeof(uintptr(0)) == 8 {
		assert.Equal(t, uintptr(88), unsafe.Sizeof(sgIOHdr{}))
	} else {
		assert.Equal(t, uintptr(64), unsafe.Sizeof(sgIOHdr{}))
	}
}

func TestATACDB(t *testing.T) {
	cases := map[string]struct {
		cmd  smartdecode.ATACommand
		want [16]byte
	}{
		"identify": {smartdecode.IdentifyCommand(),
			[16]byte{0x85, 0x08, 0x0E, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0x40, 0xEC, 0}},
		"smart read data": {smartdecode.SmartReadDataCommand(),
			[16]byte{0x85, 0x08, 0x0E, 0, 0xD0, 0, 1, 0, 0, 0, 0x4F, 0, 0xC2, 0x40, 0xB0, 0}},
		"smart read thresholds": {smartdecode.SmartReadThresholdsCommand(),
			[16]byte{0x85, 0x08, 0x0E, 0, 0xD1, 0, 1, 0, 0, 0, 0x4F, 0, 0xC2, 0x40, 0xB0, 0}},
		"smart return status": {smartdecode.SmartReturnStatusCommand(),
			[16]byte{0x85, 0x06, 0x20, 0, 0xDA, 0, 0, 0, 0, 0, 0x4F, 0, 0xC2, 0x40, 0xB0, 0}},
		"check power mode": {smartdecode.CheckPowerModeCommand(),
			[16]byte{0x85, 0x06, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x40, 0xE5, 0}},
		"read log ext": {smartdecode.DeviceStatisticsCommand(5),
			[16]byte{0x85, 0x09, 0x0E, 0, 0, 0, 1, 0, 4, 0, 5, 0, 0, 0x40, 0x2F, 0}}, // EXTEND set: a 48-bit command
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { assert.Equal(t, c.want, ataCDB(c.cmd)) })
	}
}

// fakeSG answers SG_IO calls: it fills the data and sense buffers the header points to.
type fakeSG struct {
	calls  int
	hdr    sgIOHdr
	cdb    [16]byte
	data   []byte
	sense  []byte
	status uint8
	host   uint16
	driver uint16
	resid  int32
	err    error
	block  chan struct{}
}

func (f *fakeSG) ioctl(_ int, req uintptr, arg unsafe.Pointer) (int, error) {
	f.calls++
	if f.block != nil {
		<-f.block
	}
	hdr := (*sgIOHdr)(arg)
	f.hdr = *hdr
	copy(f.cdb[:], unsafe.Slice((*byte)(hdr.cmdp), int(hdr.cmdLen)))
	if req != sgIO {
		return 0, unix.ENOTTY
	}
	if f.err != nil {
		return -1, f.err
	}
	if hdr.dxferp != nil {
		copy(unsafe.Slice((*byte)(hdr.dxferp), int(hdr.dxferLen)), f.data)
	}
	if len(f.sense) > 0 {
		copy(unsafe.Slice((*byte)(hdr.sbp), int(hdr.mxSbLen)), f.sense)
		hdr.sbLenWr = uint8(len(f.sense))
	}
	hdr.status, hdr.hostStatus, hdr.driverStatus, hdr.resid = f.status, f.host, f.driver, f.resid
	return 0, nil
}

func newSG(f *fakeSG) *sgTransport {
	return &sgTransport{fd: 7, timeout: 5 * time.Second, ioctl: f.ioctl}
}

func registerSense(mid, high uint8) []byte {
	sense := make([]byte, 32)
	sense[0], sense[7] = 0x72, 14
	d := sense[8:]
	d[0], d[1], d[9], d[11], d[13] = 0x09, 0x0C, mid, high, 0x50
	return sense
}

func TestSG_PIODataInFillsTheHeaderAndReturnsTheData(t *testing.T) {
	f := &fakeSG{data: []byte{1, 2, 3, 4}}
	tr := newSG(f)
	cmd := smartdecode.IdentifyCommand()

	res, err := tr.ATA(cmd)

	require.NoError(t, err)
	require.Len(t, res.Data, 512)
	assert.Equal(t, []byte{1, 2, 3, 4}, res.Data[:4])
	assert.Equal(t, int32('S'), f.hdr.interfaceID)
	assert.Equal(t, int32(sgDxferFromDev), f.hdr.dxferDirection)
	assert.Equal(t, uint8(16), f.hdr.cmdLen)
	assert.Equal(t, uint8(32), f.hdr.mxSbLen)
	assert.Equal(t, uint32(512), f.hdr.dxferLen)
	assert.Equal(t, uint32(5000), f.hdr.timeout)
	assert.Equal(t, ataCDB(cmd), f.cdb)
}

func TestSG_ResidShortensTheData(t *testing.T) {
	f := &fakeSG{data: make([]byte, 512), resid: 12}

	res, err := newSG(f).ATA(smartdecode.IdentifyCommand())

	require.NoError(t, err)
	assert.Len(t, res.Data, 500)
}

func TestSG_NonDataCommandReturnsTheRegistersFromTheSense(t *testing.T) {
	f := &fakeSG{status: scsiCheckCond, sense: registerSense(0xF4, 0x2C)}

	res, err := newSG(f).ATA(smartdecode.SmartReturnStatusCommand())

	require.NoError(t, err)
	assert.True(t, res.HaveRegisters)
	assert.Equal(t, uint8(0xF4), res.Registers.LBAMid)
	assert.Equal(t, uint8(0x2C), res.Registers.LBAHigh)
	assert.Equal(t, int32(sgDxferNone), f.hdr.dxferDirection)
	assert.Zero(t, f.hdr.dxferLen)
}

func TestSG_GoodStatusWithoutSenseGivesNoRegisters(t *testing.T) {
	res, err := newSG(&fakeSG{}).ATA(smartdecode.CheckPowerModeCommand())

	require.NoError(t, err)
	assert.False(t, res.HaveRegisters)
}

func TestSG_CheckConditionWithoutRegistersMeansTheDeviceRefusesSAT(t *testing.T) {
	sense := make([]byte, 32)
	sense[0], sense[2] = 0x70, 0x05
	f := &fakeSG{status: scsiCheckCond, sense: sense}

	_, err := newSG(f).ATA(smartdecode.IdentifyCommand())

	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)
}

func TestSG_TimeoutStatusesAreTimeouts(t *testing.T) {
	for name, f := range map[string]*fakeSG{
		"host":   {host: didTimeOut},
		"driver": {driver: driverTimeout},
	} {
		_, err := newSG(f).ATA(smartdecode.IdentifyCommand())

		assert.ErrorIs(t, err, smartdecode.ErrTimeout, name)
	}
}

func TestSG_OtherHostAndScsiStatusesAreErrors(t *testing.T) {
	_, err := newSG(&fakeSG{host: 1}).ATA(smartdecode.IdentifyCommand())
	require.Error(t, err)
	assert.NotErrorIs(t, err, smartdecode.ErrTimeout)

	_, err = newSG(&fakeSG{status: 0x08}).ATA(smartdecode.IdentifyCommand())
	require.Error(t, err)
}

func TestSG_ErrnoMapping(t *testing.T) {
	cases := map[error]error{
		unix.EPERM:     smartdecode.ErrPermission,
		unix.EACCES:    smartdecode.ErrPermission,
		unix.ETIMEDOUT: smartdecode.ErrTimeout,
		unix.ENOTTY:    smartdecode.ErrUnsupported,
		unix.EINVAL:    smartdecode.ErrUnsupported,
		unix.ENODEV:    smartdecode.ErrUnsupported,
	}
	for errno, want := range cases {
		_, err := newSG(&fakeSG{err: errno}).ATA(smartdecode.IdentifyCommand())

		assert.ErrorIs(t, err, want, errno.Error())
	}
	_, err := newSG(&fakeSG{err: unix.EIO}).ATA(smartdecode.IdentifyCommand())
	require.Error(t, err)
	assert.NotErrorIs(t, err, smartdecode.ErrUnsupported)
}

func TestSG_OnlyTheSixListedCommandsReachTheIoctl(t *testing.T) {
	f := &fakeSG{}
	tr := newSG(f)
	templates := []smartdecode.ATACommand{
		smartdecode.IdentifyCommand(), smartdecode.SmartReadDataCommand(), smartdecode.SmartReadThresholdsCommand(),
		smartdecode.SmartReturnStatusCommand(), smartdecode.CheckPowerModeCommand(), smartdecode.DeviceStatisticsCommand(5),
	}

	// Each allowed command is the template for every command and feature byte. The six commands pass once
	// each as a template, and the two SMART reads pass again when the feature byte names the other.
	for _, template := range templates {
		for command := 0; command < 256; command++ {
			for features := 0; features < 256; features++ {
				cmd := template
				cmd.Command, cmd.Features = uint8(command), uint8(features)
				_, _ = tr.ATA(cmd)
			}
		}
	}

	assert.Equal(t, 8, f.calls, "six commands, and SMART READ DATA and THRESHOLDS reached again through each other's template")
}

func TestSG_SmartEnableAndTheOfflineTestsNeverReachTheIoctl(t *testing.T) {
	f := &fakeSG{}
	tr := newSG(f)

	for _, features := range []uint8{0xD8, 0xD9, 0xD2, 0xD4, 0xD3} {
		cmd := smartdecode.SmartReadDataCommand()
		cmd.Features = features
		_, err := tr.ATA(cmd)

		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, "feature %#x", features)
	}
	assert.Zero(t, f.calls)
}

func TestSG_AnIoctlThatNeverReturnsGivesATimeout(t *testing.T) {
	old := timeoutGuard
	timeoutGuard = 20 * time.Millisecond
	defer func() { timeoutGuard = old }()
	f := &fakeSG{block: make(chan struct{})}
	defer close(f.block)
	tr := &sgTransport{fd: 7, timeout: 10 * time.Millisecond, ioctl: f.ioctl}

	_, err := tr.ATA(smartdecode.IdentifyCommand())

	assert.True(t, errors.Is(err, smartdecode.ErrTimeout))
}

func TestSG_ADeviceWithAHungIoctlIsNotSentAnotherCommandUntilItReturns(t *testing.T) {
	old := timeoutGuard
	timeoutGuard = 10 * time.Millisecond
	defer func() { timeoutGuard = old }()
	f := &fakeSG{block: make(chan struct{})}
	in := &inflight{}
	tr := &sgTransport{fd: 7, timeout: 10 * time.Millisecond, ioctl: f.ioctl, inflight: in}

	_, err := tr.ATA(smartdecode.IdentifyCommand())
	require.ErrorIs(t, err, smartdecode.ErrTimeout)
	_, err = tr.ATA(smartdecode.IdentifyCommand())

	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
	assert.Contains(t, err.Error(), "has not returned")
	assert.True(t, in.held())
	close(f.block)
	assert.Eventually(t, func() bool { return !in.held() }, 5*time.Second, 10*time.Millisecond)
	_, err = tr.ATA(smartdecode.IdentifyCommand())
	assert.NoError(t, err, "the device is read again once the ioctl came back")
}

func TestSG_ReadLogExtSetsTheExtendFlagAndNoOtherCommandDoes(t *testing.T) {
	for name, cmd := range map[string]smartdecode.ATACommand{
		"identify": smartdecode.IdentifyCommand(), "read data": smartdecode.SmartReadDataCommand(),
		"thresholds": smartdecode.SmartReadThresholdsCommand(), "status": smartdecode.SmartReturnStatusCommand(),
		"power mode": smartdecode.CheckPowerModeCommand(), "read log ext": smartdecode.DeviceStatisticsCommand(5),
	} {
		assert.Equal(t, name == "read log ext", ataCDB(cmd)[1]&extendBit != 0, name)
	}
}
