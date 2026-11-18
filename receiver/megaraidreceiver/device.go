// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Failures that end the read of a controller; none is retried inside a scrape.
var (
	errNeedsCapability = errors.New("the driver refused the call: the collector needs CAP_SYS_ADMIN (run it as root or grant the capability)")
	errNodeAccess      = errors.New("cannot open the megaraid_sas ioctl node: the collector needs file access to it (owner, group and mode) and CAP_SYS_ADMIN; a non-root user needs both")
	errNodeMissing     = errors.New("the megaraid_sas ioctl node does not exist: load the driver, or set create_node: true to make the node")
	errControllerGone  = errors.New("the controller is in a critical state or unloading (ENODEV)")
	errControllerBusy  = errors.New("the controller is busy (EBUSY)")
	errRejected        = errors.New("the driver rejected the request")
	errInterrupted     = errors.New("the call was interrupted")
	errTimeout         = errors.New("the controller did not answer in time")
	errDeviceBroken    = errors.New("an earlier call timed out, so the device is not used again")
	errStillInFlight   = errors.New("an earlier call to the controller has not returned")
)

// response is the command status, a copy of the data the controller returned and, for a
// pass-through, the sense bytes.
type response struct {
	status uint8
	data   []byte
	sense  []byte
}

// device sends one allowed frame at a time.
type device interface {
	exec(req request) (response, error)
	pass(req passRequest) (response, error)
}

// skipsScrape reports whether an error from exec ends the read of the controller for this scrape
// and is logged once per episode.
func skipsScrape(err error) bool {
	return errors.Is(err, errNeedsCapability) || errors.Is(err, errNodeAccess) || errors.Is(err, errNodeMissing) || errors.Is(err, errStillInFlight) || errors.Is(err, errControllerGone) ||
		errors.Is(err, errControllerBusy) || errors.Is(err, errInterrupted) || errors.Is(err, errTimeout) ||
		errors.Is(err, errDeviceBroken) || errors.Is(err, errRejected)
}

// mapOpenError turns the error of opening the node into a skip class. EACCES on open can be the
// file mode as well as the missing capability, so the message carries the node's owner and mode
// (nodeInfo) and names both; EPERM is the capability.
func mapOpenError(err error, nodeInfo string) error {
	switch {
	case errors.Is(err, syscall.EACCES):
		return fmt.Errorf("%w (node %s): %v", errNodeAccess, nodeInfo, err)
	case errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %v", errNeedsCapability, err)
	}
	return fmt.Errorf("open the ioctl node: %w", err)
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
// and the buffers; the kernel reads the buffer addresses from the packet, and the buffers are
// passed along only so they stay reachable during the call. The file with the syscall is
// ioctl_linux.go; this type is tested with a fake ioctl function.
type ioctlDevice struct {
	hostNo  uint16
	ioctl   func(packet []byte, buffers ...[]byte) error
	timeout time.Duration

	mu       sync.Mutex
	broken   bool
	finished chan struct{}
}

// hangReporter is implemented by a device whose call timed out: hung returns a channel that is
// closed when the kernel returns from that call, and nil while no call is outstanding.
type hangReporter interface{ hung() <-chan struct{} }

func (d *ioctlDevice) hung() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.finished
}

// exec sends one DCMD read.
func (d *ioctlDevice) exec(req request) (response, error) {
	if req.dataLen < 1 || req.dataLen > maxDataLen {
		return response{}, fmt.Errorf("buffer length %d is outside 1..%d", req.dataLen, maxDataLen)
	}
	return d.call(req.dataLen, 0, checkPacket, func(buf, _ uintptr) []byte {
		return buildPacket(d.hostNo, req, buf)
	})
}

// pass sends one pass-through read to a drive.
func (d *ioctlDevice) pass(req passRequest) (response, error) {
	if req.dataLen < 0 || req.dataLen > maxPassData {
		return response{}, fmt.Errorf("buffer length %d is outside 0..%d", req.dataLen, maxPassData)
	}
	return d.call(req.dataLen, senseLen, checkPassPacket, func(buf, sense uintptr) []byte {
		return buildPassPacket(d.hostNo, req, buf, sense)
	})
}

// call sends one request through check and waits at most timeout. After a timeout the kernel may
// still write the buffers, so they stay allocated until the call returns; a waiter then frees them
// and closes the channel hung returns. Until then the device refuses further calls.
func (d *ioctlDevice) call(dataLen, senseBytes int, check func([]byte) error, build func(buf, sense uintptr) []byte) (response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.broken {
		return response{}, errDeviceBroken
	}
	var buf, sense []byte
	var bufAddr, senseAddr uintptr
	var err error
	release := func() {
		if buf != nil {
			freeBuffer(buf)
		}
		if sense != nil {
			freeBuffer(sense)
		}
	}
	if dataLen > 0 {
		if buf, err = allocBuffer(dataLen); err != nil {
			return response{}, err
		}
		bufAddr = uintptr(unsafe.Pointer(&buf[0]))
	}
	if senseBytes > 0 {
		if sense, err = allocBuffer(senseBytes); err != nil {
			release()
			return response{}, err
		}
		senseAddr = uintptr(unsafe.Pointer(&sense[0]))
	}
	packet := build(bufAddr, senseAddr)
	if err := check(packet); err != nil {
		release()
		return response{}, fmt.Errorf("refused to send: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- d.ioctl(packet, buf, sense) }()
	timer := time.NewTimer(d.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			release()
			return response{}, mapErrno(err)
		}
	case <-timer.C:
		d.broken = true
		finished := make(chan struct{})
		d.finished = finished
		go func() {
			<-done //nolint:errcheck // the error of a call nobody waits for any more
			release()
			close(finished)
		}()
		return response{}, errTimeout
	}
	resp := response{status: packetStatus(packet), data: append([]byte(nil), buf...), sense: append([]byte(nil), sense...)}
	release()
	return resp, nil
}
