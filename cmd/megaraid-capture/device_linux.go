// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const callTimeout = 5 * time.Second

// systemNodeOps is the real file access of the node probe.
var systemNodeOps = nodeOps{
	stat: func(path string) (uint32, error) {
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil {
			return 0, err
		}
		return st.Mode, nil
	},
	open: func(path string) (int, error) {
		return syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	},
	fstat: func(fd int) (uint32, error) {
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); err != nil {
			return 0, err
		}
		return st.Mode, nil
	},
	close: func(fd int) { _ = syscall.Close(fd) },
}

// openNode opens the ioctl node for one controller: devPath when given, else the first probe
// path that is a character device. The driver refuses the open without CAP_SYS_ADMIN, whatever
// the file mode says.
func openNode(devPath string, hostNo int) (device, func(), error) {
	fd, path, err := selectNode(systemNodeOps, devPath)
	if err != nil {
		return nil, nil, err
	}
	dev := &ioctlDevice{
		hostNo:  uint16(hostNo), //nolint:gosec // host numbers are small
		node:    path,
		timeout: callTimeout,
		ioctl: func(packet, buf []byte) error {
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ioctlFirmware, uintptr(unsafe.Pointer(&packet[0])))
			runtime.KeepAlive(packet)
			runtime.KeepAlive(buf)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}
	return dev, func() { _ = syscall.Close(fd) }, nil
}
