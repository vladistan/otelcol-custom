// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Failures that end the capture of a controller; none is retried.
var (
	errNeedsCapability = errors.New("the ioctl node needs CAP_SYS_ADMIN: run the tool as root")
	errControllerGone  = errors.New("the controller is in a critical state or unloading (ENODEV)")
	errControllerBusy  = errors.New("the controller is busy (EBUSY)")
	errRejected        = errors.New("the driver rejected the request")
	errInterrupted     = errors.New("the call was interrupted")
	errTimeout         = errors.New("the controller did not answer in time")
	errDeviceBroken    = errors.New("an earlier call timed out, so the device is not used again")
)

// response is the command status and a copy of the data the controller returned.
type response struct {
	status uint8
	data   []byte
}

// device sends one allowed DCMD read at a time.
type device interface {
	exec(req request) (response, error)
}

// probePaths are the ioctl nodes tried, in order, when -dev is not given. Drivers and udev rules
// differ in which name they create.
var probePaths = []string{"/dev/megaraid_sas_ioctl_node", "/dev/megaraid_sas"}

// nodeOps is the file access of the node probe, replaceable in tests. Modes are st_mode values.
type nodeOps struct {
	stat  func(path string) (uint32, error)
	open  func(path string) (int, error)
	fstat func(fd int) (uint32, error)
	close func(fd int)
}

func isCharDevice(mode uint32) bool { return mode&syscall.S_IFMT == syscall.S_IFCHR }

func notCharError(path string, mode uint32) error {
	return fmt.Errorf("%s is not a character device (mode %#o); refusing to open it", path, mode)
}

// selectNode opens the ioctl node and returns its descriptor and path. With explicit set only that
// path is tried; otherwise the first probe path that exists and is a character device wins. A
// path that is not a character device is never used, and the opened descriptor is checked again.
func selectNode(ops nodeOps, explicit string) (int, string, error) {
	paths := probePaths
	if explicit != "" {
		paths = []string{explicit}
	}
	var tried []string
	for _, p := range paths {
		mode, err := ops.stat(p)
		switch {
		case errors.Is(err, syscall.ENOENT):
			tried = append(tried, p+" (missing)")
			continue
		case err != nil:
			if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
				return -1, "", fmt.Errorf("%w: %v", errNeedsCapability, err)
			}
			return -1, "", fmt.Errorf("stat %s: %w", p, err)
		case !isCharDevice(mode):
			if explicit != "" {
				return -1, "", notCharError(p, mode)
			}
			tried = append(tried, fmt.Sprintf("%s (not a character device, mode %#o)", p, mode))
			continue
		}
		fd, err := ops.open(p)
		switch {
		case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
			return -1, "", fmt.Errorf("%w: %v", errNeedsCapability, err)
		case err != nil:
			return -1, "", fmt.Errorf("open %s: %w", p, err)
		}
		opened, err := ops.fstat(fd)
		if err != nil {
			ops.close(fd)
			return -1, "", fmt.Errorf("fstat %s: %w", p, err)
		}
		if !isCharDevice(opened) {
			ops.close(fd)
			return -1, "", notCharError(p, opened)
		}
		return fd, p, nil
	}
	return -1, "", fmt.Errorf("no megaraid_sas ioctl node found; tried %s; load the megaraid_sas driver or pass -dev", strings.Join(tried, ", "))
}

func mapErrno(err error) error {
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %v", errNeedsCapability, err)
	case errors.Is(err, syscall.ENODEV):
		return fmt.Errorf("%w: %v", errControllerGone, err)
	case errors.Is(err, syscall.EBUSY):
		return fmt.Errorf("%w: %v", errControllerBusy, err)
	case errors.Is(err, syscall.EINTR):
		return fmt.Errorf("%w: %v", errInterrupted, err)
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOTTY):
		return fmt.Errorf("%w: %v", errRejected, err)
	}
	return fmt.Errorf("ioctl failed: %w", err)
}

// ioctlDevice talks to one controller through the driver's ioctl node. ioctl receives the packet
// and the data buffer; the kernel reads the buffer address from the packet, and the buffer is
// passed along only so it stays reachable during the call.
type ioctlDevice struct {
	hostNo  uint16
	node    string
	ioctl   func(packet, buf []byte) error
	timeout time.Duration

	mu     sync.Mutex
	broken bool
}

// nodePath is the device path the ioctl descriptor was opened from.
func (d *ioctlDevice) nodePath() string { return d.node }

// exec sends one request and waits at most timeout. After a timeout the kernel may still write
// the buffer, so the buffer is left allocated and the device refuses further calls.
func (d *ioctlDevice) exec(req request) (response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.broken {
		return response{}, errDeviceBroken
	}
	if req.dataLen < 1 || req.dataLen > maxDataLen {
		return response{}, fmt.Errorf("buffer length %d is outside 1..%d", req.dataLen, maxDataLen)
	}
	buf, err := allocBuffer(req.dataLen)
	if err != nil {
		return response{}, err
	}
	packet := buildPacket(d.hostNo, req, uintptr(unsafe.Pointer(&buf[0])))
	if err := checkPacket(packet); err != nil {
		freeBuffer(buf)
		return response{}, fmt.Errorf("refused to send: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- d.ioctl(packet, buf) }()
	timer := time.NewTimer(d.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			freeBuffer(buf)
			return response{}, mapErrno(err)
		}
	case <-timer.C:
		d.broken = true
		return response{}, errTimeout
	}
	resp := response{status: packetStatus(packet), data: append([]byte(nil), buf...)}
	freeBuffer(buf)
	return resp, nil
}
