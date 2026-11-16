// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"encoding/binary"
	"errors"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// answer returns an ioctl function that writes data into the first buffer and sets the status.
func answer(status uint8, data []byte) func([]byte, ...[]byte) error {
	return func(packet []byte, buffers ...[]byte) error {
		packet[frameOff+fStatus] = status
		copy(buffers[0], data)
		return nil
	}
}

func newIoctlDevice(fn func([]byte, ...[]byte) error) *ioctlDevice {
	return &ioctlDevice{hostNo: 2, ioctl: fn, timeout: time.Second}
}

func TestExecReturnsTheStatusAndACopyOfTheData(t *testing.T) {
	d := newIoctlDevice(answer(3, []byte{1, 2, 3, 4}))

	resp, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: 8})

	require.NoError(t, err)
	assert.Equal(t, uint8(3), resp.status)
	assert.Equal(t, []byte{1, 2, 3, 4, 0, 0, 0, 0}, resp.data)
}

func TestExecRefusesAnOpcodeOutsideTheListBeforeTheKernel(t *testing.T) {
	var calls atomic.Int32
	d := newIoctlDevice(func([]byte, ...[]byte) error { calls.Add(1); return nil })

	_, err := d.exec(request{opcode: 0x04030000, dataLen: 8}) // CFG_CLEAR

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refused to send")
	assert.Zero(t, calls.Load())
}

func TestExecRefusesBadBufferLengths(t *testing.T) {
	d := newIoctlDevice(answer(0, nil))

	for _, n := range []int{0, -1, maxDataLen + 1} {
		_, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: n})
		assert.Error(t, err, n)
	}
	_, err := d.pass(passRequest{cdbLen: 16, dataLen: maxPassData + 1})
	assert.Error(t, err)
}

func TestABlockedCallEndsAtTheLimitAndStartsNoSecond(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	d := newIoctlDevice(func([]byte, ...[]byte) error { calls.Add(1); <-release; return nil })
	d.timeout = 50 * time.Millisecond
	defer close(release)

	start := time.Now()
	_, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: 8})

	require.ErrorIs(t, err, errTimeout)
	assert.Less(t, time.Since(start), 2*time.Second)
	_, err = d.exec(request{opcode: opCtrlGetInfo, dataLen: 8})
	require.ErrorIs(t, err, errDeviceBroken)
	assert.Equal(t, int32(1), calls.Load(), "the broken device sends nothing more")
}

func TestTheBufferSurvivesGarbageCollectionDuringTheCall(t *testing.T) {
	d := newIoctlDevice(func(packet []byte, buffers ...[]byte) error {
		for range 3 {
			runtime.GC()
		}
		addr := uintptr(binary.LittleEndian.Uint64(packet[sglArrayOff:]))
		require.Equal(t, uintptr(unsafe.Pointer(&buffers[0][0])), addr, "the packet carries the buffer address")
		for i := range buffers[0] {
			buffers[0][i] = 0xA5
		}
		runtime.GC()
		return nil
	})

	resp, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: 4096})

	require.NoError(t, err)
	for _, b := range resp.data {
		require.Equal(t, byte(0xA5), b)
	}
}

func TestErrnosMapToTheSkipClasses(t *testing.T) {
	tests := map[string]struct {
		errno syscall.Errno
		want  error
	}{
		"no capability": {syscall.EACCES, errNeedsCapability},
		"not permitted": {syscall.EPERM, errNeedsCapability},
		"controller":    {syscall.ENODEV, errControllerGone},
		"busy":          {syscall.EBUSY, errControllerBusy},
		"interrupted":   {syscall.EINTR, errInterrupted},
		"invalid":       {syscall.EINVAL, errRejected},
		"not a tty":     {syscall.ENOTTY, errRejected},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := newIoctlDevice(func([]byte, ...[]byte) error { return tt.errno })

			_, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: 8})

			require.ErrorIs(t, err, tt.want)
			assert.True(t, skipsScrape(err))
		})
	}
	other := mapErrno(syscall.EIO)
	assert.False(t, skipsScrape(other))
	assert.Contains(t, other.Error(), "ioctl failed")
}

func TestPassSendsASenseBufferAndReturnsIt(t *testing.T) {
	d := newIoctlDevice(func(packet []byte, buffers ...[]byte) error {
		require.Len(t, buffers, 2)
		copy(buffers[1], []byte{0x72, 0x00})
		copy(buffers[0], []byte{9})
		return nil
	})
	cdb := ataCDB(smartdecode.IdentifyCommand())

	resp, err := d.pass(passRequest{target: 4, cdb: cdb, cdbLen: 16, dataLen: 512})

	require.NoError(t, err)
	assert.Equal(t, byte(9), resp.data[0])
	assert.Equal(t, byte(0x72), resp.sense[0])
}

func TestPassWithoutDataAllocatesNoDataBuffer(t *testing.T) {
	d := newIoctlDevice(func(packet []byte, buffers ...[]byte) error {
		assert.Nil(t, buffers[0])
		return nil
	})
	cdb := ataCDB(smartdecode.CheckPowerModeCommand())

	_, err := d.pass(passRequest{target: 1, cdb: cdb, cdbLen: 16})

	require.NoError(t, err)
}

func TestMapErrnoWrapsUnknownErrors(t *testing.T) {
	assert.True(t, errors.Is(mapErrno(syscall.ENODEV), errControllerGone))
}
