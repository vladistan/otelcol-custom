// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// inflight marks a device whose ioctl has not returned. A transport takes it before the ioctl, and the
// goroutine that issues the ioctl releases it when the call comes back, so a device that hangs holds
// one goroutine and is left alone until it answers, instead of leaking one goroutine per interval. A
// nil inflight never blocks.
type inflight struct{ busy atomic.Bool }

func (f *inflight) acquire() bool { return f == nil || f.busy.CompareAndSwap(false, true) }

func (f *inflight) release() {
	if f != nil {
		f.busy.Store(false)
	}
}

func (f *inflight) held() bool { return f != nil && f.busy.Load() }

// errStillRunning is what a transport returns for a device that has a command in the kernel.
func errStillRunning() error {
	return fmt.Errorf("%w: a command sent to the device earlier has not returned", smartdecode.ErrTimeout)
}

// mapOpenErr maps the error of opening a device file. A refusal here is a missing file permission, not
// a missing capability: the process needs read access to the file before the ioctl is tried.
func mapOpenErr(err error) error {
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: %w: %v", smartdecode.ErrFileAccess, smartdecode.ErrPermission, err)
	}
	return mapErrno(err)
}

// ioctlFunc issues one ioctl and returns the value the call returned: an NVMe admin command returns its
// status there. Tests replace it.
type ioctlFunc func(fd int, req uintptr, arg unsafe.Pointer) (int, error)

// sysIoctl issues an ioctl through x/sys/unix, retrying when a signal interrupts it.
func sysIoctl(fd int, req uintptr, arg unsafe.Pointer) (int, error) {
	for {
		r, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		switch errno {
		case 0:
			return int(r), nil
		case unix.EINTR:
			continue
		default:
			return int(r), errno
		}
	}
}

// mapErrno turns the errors of the open and ioctl calls into the decoder errors callers match on.
func mapErrno(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES), errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %v", smartdecode.ErrPermission, err)
	case errors.Is(err, unix.ETIMEDOUT):
		return fmt.Errorf("%w: %v", smartdecode.ErrTimeout, err)
	case errors.Is(err, unix.ENOTTY), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENODEV),
		errors.Is(err, unix.ENXIO), errors.Is(err, unix.EOPNOTSUPP):
		return fmt.Errorf("%w: %v", smartdecode.ErrUnsupported, err)
	}
	return err
}
