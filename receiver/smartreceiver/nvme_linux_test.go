// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// fakeAdmin answers NVMe admin ioctls: it copies its data to the address in the command.
type fakeAdmin struct {
	calls  int
	req    nvmeAdminCmd
	data   []byte
	status int
	err    error
}

func (f *fakeAdmin) ioctl(_ int, req uintptr, arg unsafe.Pointer) (int, error) {
	f.calls++
	cmd := (*nvmeAdminCmd)(arg)
	f.req = *cmd
	if req != nvmeIoctlAdminCmd {
		return 0, unix.ENOTTY
	}
	if f.err != nil {
		return -1, f.err
	}
	return f.status, nil
}

func newAdmin(f *fakeAdmin) *nvmeTransport {
	return &nvmeTransport{fd: 9, timeout: 3 * time.Second, ioctl: f.ioctl, fill: func(data []byte) { copy(data, f.data) }}
}

func TestAdmin_IdentifyControllerFillsTheCommand(t *testing.T) {
	f := &fakeAdmin{data: []byte{7, 8, 9}}

	got, err := newAdmin(f).Admin(smartdecode.IdentifyControllerCommand())

	require.NoError(t, err)
	require.Len(t, got, 4096)
	assert.Equal(t, []byte{7, 8, 9}, got[:3])
	assert.Equal(t, uint8(0x06), f.req.opcode)
	assert.Equal(t, uint32(1), f.req.cdw10)
	assert.Equal(t, uint32(4096), f.req.dataLen)
	assert.Equal(t, uint32(3000), f.req.timeoutMS)
}

func TestAdmin_SmartLogCommand(t *testing.T) {
	f := &fakeAdmin{data: make([]byte, 512)}

	got, err := newAdmin(f).Admin(smartdecode.SmartLogCommand())

	require.NoError(t, err)
	assert.Len(t, got, 512)
	assert.Equal(t, uint8(0x02), f.req.opcode)
	assert.Equal(t, uint32(0xFFFFFFFF), f.req.nsid)
	assert.Equal(t, uint32(127<<16|0x02), f.req.cdw10)
}

func TestAdmin_OnlyIdentifyControllerAndTheSmartLogReachTheIoctl(t *testing.T) {
	f := &fakeAdmin{}
	tr := newAdmin(f)

	for _, template := range []smartdecode.NVMeCommand{smartdecode.IdentifyControllerCommand(), smartdecode.SmartLogCommand()} {
		for opcode := 0; opcode < 256; opcode++ {
			for low := uint32(0); low < 256; low++ {
				cmd := template
				cmd.Opcode = uint8(opcode)
				cmd.CDW10 = template.CDW10&^0xFF | low
				_, _ = tr.Admin(cmd)
			}
		}
	}

	assert.Equal(t, 2, f.calls, "opcode 06h with CNS 01h, and opcode 02h with log id 02h")
}

func TestAdmin_ADataLengthOrALogLengthOffTheListNeverReachesTheIoctl(t *testing.T) {
	f := &fakeAdmin{}
	tr := newAdmin(f)

	for _, dataLen := range []int{-1, 0, 8, 511, 513, 1 << 30} {
		cmd := smartdecode.SmartLogCommand()
		cmd.DataLen = dataLen
		_, err := tr.Admin(cmd)
		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, "data length %d", dataLen)
	}
	cmd := smartdecode.SmartLogCommand()
	cmd.CDW10 = 1023<<16 | 0x02 // asks for 4 KiB into a 512 byte buffer
	_, err := tr.Admin(cmd)
	assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed)
	assert.Zero(t, f.calls)
}

func TestAdmin_TheBufferStaysAliveUntilALateIoctlReturns(t *testing.T) {
	old := timeoutGuard
	timeoutGuard = 10 * time.Millisecond
	defer func() { timeoutGuard = old }()
	release := make(chan struct{})
	freed := make(chan struct{})
	tr := &nvmeTransport{
		fd: 9, timeout: 10 * time.Millisecond,
		// The kernel keeps only the address, as a number, and writes to it when the command ends.
		ioctl: func(int, uintptr, unsafe.Pointer) (int, error) { <-release; return 0, nil },
		fill:  func(data []byte) { runtime.SetFinalizer(&data[0], func(*byte) { close(freed) }) },
	}

	_, err := tr.Admin(smartdecode.SmartLogCommand())
	require.ErrorIs(t, err, smartdecode.ErrTimeout)
	for range 5 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-freed:
		t.Fatal("the data buffer was collected while the ioctl had not returned")
	default:
	}
	close(release)
	assert.Eventually(t, func() bool {
		runtime.GC()
		select {
		case <-freed:
			return true
		default:
			return false
		}
	}, 5*time.Second, 20*time.Millisecond, "the buffer is released once the ioctl came back")
}

func TestAdmin_ADeviceWithAHungIoctlIsNotSentAnotherCommandUntilItReturns(t *testing.T) {
	old := timeoutGuard
	timeoutGuard = 10 * time.Millisecond
	defer func() { timeoutGuard = old }()
	release := make(chan struct{})
	var calls atomic.Int32
	in := &inflight{}
	tr := &nvmeTransport{
		fd: 9, timeout: 10 * time.Millisecond, inflight: in,
		ioctl: func(int, uintptr, unsafe.Pointer) (int, error) { calls.Add(1); <-release; return 0, nil },
	}

	_, err := tr.Admin(smartdecode.SmartLogCommand())
	require.ErrorIs(t, err, smartdecode.ErrTimeout)
	_, err = tr.Admin(smartdecode.SmartLogCommand())

	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
	assert.Contains(t, err.Error(), "has not returned")
	assert.Equal(t, int32(1), calls.Load())
	close(release)
	assert.Eventually(t, func() bool { return !in.held() }, 5*time.Second, 10*time.Millisecond)
}

func TestAdmin_ThePermissionErrnosOfAnIoctlAreCapabilitiesNotFileAccess(t *testing.T) {
	for _, errno := range []error{unix.EPERM, unix.EACCES} { // the NVMe admin ioctl gives EACCES without CAP_SYS_ADMIN
		_, err := newAdmin(&fakeAdmin{err: errno}).Admin(smartdecode.SmartLogCommand())

		assert.ErrorIs(t, err, smartdecode.ErrPermission, errno.Error())
		assert.NotErrorIs(t, err, smartdecode.ErrFileAccess, errno.Error())
	}
}

func TestAdmin_StatusAndErrnoMapping(t *testing.T) {
	_, err := newAdmin(&fakeAdmin{status: 0x01}).Admin(smartdecode.SmartLogCommand())
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)

	_, err = newAdmin(&fakeAdmin{status: 0x81}).Admin(smartdecode.SmartLogCommand())
	require.Error(t, err)
	assert.NotErrorIs(t, err, smartdecode.ErrUnsupported)

	// The Do Not Retry and More bits ride on the status; the code under them still decides.
	for _, status := range []int{0x4001, 0x2002, 0x6001} {
		_, err = newAdmin(&fakeAdmin{status: status}).Admin(smartdecode.SmartLogCommand())
		assert.ErrorIs(t, err, smartdecode.ErrUnsupported, "status %#x", status)
	}

	_, err = newAdmin(&fakeAdmin{err: unix.EPERM}).Admin(smartdecode.SmartLogCommand())
	assert.ErrorIs(t, err, smartdecode.ErrPermission)

	_, err = newAdmin(&fakeAdmin{err: unix.ENOTTY}).Admin(smartdecode.SmartLogCommand())
	assert.ErrorIs(t, err, smartdecode.ErrUnsupported)
}
