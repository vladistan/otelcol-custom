// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package windiskhealthreceiver

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// cancelGrace is how long a cancelled call may take to end before its buffers are given up as in use.
const cancelGrace = 2 * time.Second

// maxProbe is how many drive numbers discovery tries; max_devices caps what is read.
const maxProbe = 128

func newSystemBus() (bus, error) {
	return &ioctlBus{listFn: listDrives, openFn: openDrive}, nil
}

func drivePath(i int) string { return fmt.Sprintf(`\\.\PhysicalDrive%d`, i) }

func win32(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return mapWin32(uint32(errno), err)
	}
	return err
}

// listDrives probes the drive numbers with a handle that asks for no access, so nothing is sent to a disk.
// A missing drive is skipped; any other open fault is counted by error, and when no drive is found at all
// the counts are returned as the error so a real fault does not look like an empty machine.
func listDrives() ([]string, error) {
	var names []string
	faults := map[string]int{}
	for i := 0; i < maxProbe; i++ {
		p, err := windows.UTF16PtrFromString(drivePath(i))
		if err != nil {
			return nil, fmt.Errorf("drive path %d: %w", i, err)
		}
		h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		switch {
		case err == nil:
			_ = windows.CloseHandle(h)
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			continue
		default:
			faults[err.Error()]++
			continue
		}
		names = append(names, fmt.Sprintf("PhysicalDrive%d", i))
	}
	if len(names) == 0 && len(faults) > 0 {
		return nil, fmt.Errorf("no drive could be probed: %v", faults)
	}
	return names, nil
}

type handleController struct {
	h       windows.Handle
	timeout time.Duration
	// stuck keeps the buffers, overlapped structure and event of calls that the driver did not end after a
	// cancel: the kernel may still write into them, so they must not be freed or reused.
	stuck []*pendingBuffers
}

type pendingBuffers struct {
	buf []byte
	ov  windows.Overlapped
	ev  windows.Handle
}

// overlappedCall is the pendingCall of one DeviceIoControl that returned ERROR_IO_PENDING.
type overlappedCall struct {
	h  windows.Handle
	ov *windows.Overlapped
}

func (o *overlappedCall) wait(d time.Duration) (bool, error) {
	event, err := windows.WaitForSingleObject(o.ov.HEvent, timeoutMillis(d))
	if err != nil {
		return false, err
	}
	return event == windows.WAIT_OBJECT_0, nil
}

func (o *overlappedCall) cancel() { _ = windows.CancelIoEx(o.h, o.ov) }

// openDrive opens a disk with read and write access, which ATA pass-through needs; no request writes.
func openDrive(name string, timeout time.Duration) (controller, error) {
	p, err := windows.UTF16PtrFromString(`\\.\` + name)
	if err != nil {
		return nil, fmt.Errorf("drive path %s: %w", name, err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, win32(err))
	}
	return &handleController{h: h, timeout: timeout}, nil
}

func (c *handleController) Close() error { return windows.CloseHandle(c.h) }

// control sends one request and waits at most the command timeout; a call that does not finish is cancelled.
func (c *handleController) control(code uint32, in []byte, outLen int) ([]byte, error) {
	if len(in) == 0 && outLen == 0 {
		return nil, errors.New("a control request needs an input or an output buffer")
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("create the completion event: %w", err)
	}
	p := &pendingBuffers{buf: make([]byte, max(len(in), outLen)), ev: ev}
	p.ov.HEvent = ev
	copy(p.buf, in)
	var n uint32
	err = windows.DeviceIoControl(c.h, code, &p.buf[0], uint32(len(in)), &p.buf[0], uint32(outLen), &n, &p.ov)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		finished, waitErr := awaitCall(&overlappedCall{h: c.h, ov: &p.ov}, c.timeout, cancelGrace)
		if !finished {
			c.stuck = append(c.stuck, p) // the event stays open with it
			return nil, waitErr
		}
		if waitErr != nil {
			_ = windows.CloseHandle(ev)
			return nil, waitErr
		}
		err = windows.GetOverlappedResult(c.h, &p.ov, &n, false)
	}
	_ = windows.CloseHandle(ev)
	if err != nil {
		return nil, win32(err)
	}
	return p.buf[:n], nil
}
