// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// iocparm wraps the request block on FreeBSD, where the kernel reads the block through a pointer.
type iocparm struct {
	version uint32
	_       uint32
	cmd     uint64
	size    uint64
}

const (
	iocparmVersion = 15 // any value but 7 (the legacy block) selects the current layout
	iocInOut       = 0xc0000000
)

// requestCode is _IOWR('Z', vec, iocparm).
func requestCode(vec uint) uintptr {
	return uintptr(iocInOut | uint(unsafe.Sizeof(iocparm{}))<<16 | 'Z'<<8 | vec&0xff)
}

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
	param := iocparm{version: iocparmVersion, cmd: uint64(uintptr(unsafe.Pointer(&d.zc.b[0]))), size: zcmdSize}
	var errno unix.Errno
	for range 3 {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), requestCode(vec), uintptr(unsafe.Pointer(&param)))
		if errno != unix.EINTR {
			break
		}
	}
	runtime.KeepAlive(dst)
	runtime.KeepAlive(d.zc)
	reply := callReply{name: d.zc.name(), cookie: d.zc.cookie(), size: int(d.zc.dstSize())}
	if errno != 0 {
		return reply, errno
	}
	return reply, nil
}
