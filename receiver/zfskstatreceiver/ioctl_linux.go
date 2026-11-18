// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package zfskstatreceiver

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const zfsIocFirst = 'Z' << 8

func requestCode(vec uint) uintptr { return uintptr(zfsIocFirst + vec) }

type ioctlDev struct {
	fd int
	zc *zcmd
}

// openZFS opens the control device; the node is read through ioctls only.
func openZFS(path string) (zfsDevice, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &ioctlDev{fd: fd, zc: newZcmd()}, nil
}

func (d *ioctlDev) close() error { return unix.Close(d.fd) }

// call sends one request. dst stays referenced until the call returns; the Go heap does not move it.
func (d *ioctlDev) call(vec uint, name string, cookie uint64, dst []byte) (callReply, error) {
	if err := checkVec(vec); err != nil {
		return callReply{}, err
	}
	if len(dst) == 0 {
		return callReply{}, unix.EINVAL
	}
	d.zc.reset()
	if err := d.zc.setName(name); err != nil {
		return callReply{}, unix.ENAMETOOLONG
	}
	d.zc.setCookie(cookie)
	d.zc.setDst(uint64(uintptr(unsafe.Pointer(&dst[0]))), uint64(len(dst)))
	var errno unix.Errno
	for range 3 {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), requestCode(vec), uintptr(unsafe.Pointer(&d.zc.b[0])))
		if errno != unix.EINTR {
			break
		}
	}
	runtime.KeepAlive(dst)
	reply := callReply{name: d.zc.name(), cookie: d.zc.cookie(), size: int(d.zc.dstSize())}
	if errno != 0 {
		return reply, errno
	}
	return reply, nil
}
