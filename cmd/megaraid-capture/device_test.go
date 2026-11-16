// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// fakeKernel is an ioctl that answers like the driver: it reads the packet, writes canned
// bytes into the data buffer and sets the status byte of the frame.
type fakeKernel struct {
	answer func(opcode uint32, mbox []byte) (status uint8, data []byte, err error)
	seen   []uint32
	bufs   [][]byte
	// written is closed once the answer has been copied into the buffer.
	written chan struct{}
}

func (k *fakeKernel) ioctl(packet, buf []byte) error {
	opcode := binary.LittleEndian.Uint32(packet[frameOff+24:])
	k.seen = append(k.seen, opcode)
	k.bufs = append(k.bufs, buf)
	if base := binary.LittleEndian.Uint64(packet[sglArrayOff:]); base != uint64(uintptr(unsafe.Pointer(&buf[0]))) {
		return errors.New("iovec base is not the data buffer")
	}
	if n := binary.LittleEndian.Uint64(packet[sglArrayOff+8:]); n != uint64(len(buf)) {
		return errors.New("iovec length is not the buffer length")
	}
	status, data, err := k.answer(opcode, packet[frameOff+28:frameOff+40])
	if err != nil {
		return err
	}
	copy(buf, data)
	packet[frameOff+2] = status
	if k.written != nil {
		close(k.written)
	}
	return nil
}

func newTestDevice(k *fakeKernel, timeout time.Duration) *ioctlDevice {
	return &ioctlDevice{hostNo: 2, ioctl: k.ioctl, timeout: timeout}
}

func TestExecReturnsTheStatusAndACopyOfTheData(t *testing.T) {
	k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) { return 0, []byte("hello controller"), nil }}
	dev := newTestDevice(k, time.Second)

	resp, err := dev.exec(request{opcode: opCtrlGetInfo, dataLen: 32})

	if err != nil {
		t.Fatal(err)
	}
	if resp.status != 0 || len(resp.data) != 32 || !bytes.HasPrefix(resp.data, []byte("hello controller")) {
		t.Fatalf("response = %+v", resp)
	}
	if unsafe.Pointer(&resp.data[0]) == unsafe.Pointer(&k.bufs[0][0]) {
		t.Error("the response aliases the data buffer, which is released after the call")
	}
}

func TestExecPassesTheMailboxThrough(t *testing.T) {
	var got []byte
	k := &fakeKernel{answer: func(_ uint32, mbox []byte) (uint8, []byte, error) {
		got = append([]byte(nil), mbox...)
		return 0, nil, nil
	}}
	var mbox [12]byte
	mbox[0], mbox[1] = 0x34, 0x12

	_, err := newTestDevice(k, time.Second).exec(request{opcode: opPDGetInfo, mbox: mbox, dataLen: 16})

	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, mbox[:]) {
		t.Fatalf("mailbox = %x", got)
	}
}

func TestExecNeverReachesTheKernelWithADeniedOpcode(t *testing.T) {
	k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) { return 0, nil, nil }}
	dev := newTestDevice(k, time.Second)

	for name, op := range mfiregOpcodes {
		_, isAllowed := allowed[op]
		_, err := dev.exec(request{opcode: op, dataLen: 64})
		if !isAllowed && err == nil {
			t.Errorf("%s was sent", name)
		}
	}
	for _, seen := range k.seen {
		if _, ok := allowed[seen]; !ok {
			t.Errorf("the kernel saw the opcode %#x", seen)
		}
	}
}

func TestExecRefusesBadLengths(t *testing.T) {
	dev := newTestDevice(&fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) { return 0, nil, nil }}, time.Second)

	for _, n := range []int{0, -1, maxDataLen + 1} {
		if _, err := dev.exec(request{opcode: opCtrlGetInfo, dataLen: n}); err == nil {
			t.Errorf("length %d was accepted", n)
		}
	}
}

func TestExecMapsKernelErrorsToNamedFailures(t *testing.T) {
	tests := map[syscall.Errno]error{
		syscall.EACCES: errNeedsCapability,
		syscall.EPERM:  errNeedsCapability,
		syscall.ENODEV: errControllerGone,
		syscall.EBUSY:  errControllerBusy,
		syscall.EINVAL: errRejected,
		syscall.EINTR:  errInterrupted,
		syscall.ENOTTY: errRejected,
	}
	for errno, want := range tests {
		k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) { return 0, nil, errno }}

		_, err := newTestDevice(k, time.Second).exec(request{opcode: opCtrlGetInfo, dataLen: 8})

		if !errors.Is(err, want) {
			t.Errorf("%v: got %v, want %v", errno, err, want)
		}
	}
}

func TestExecTimeoutReturnsAndBreaksTheDevice(t *testing.T) {
	release := make(chan struct{})
	k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) {
		<-release
		return 0, nil, nil
	}}
	dev := newTestDevice(k, 50*time.Millisecond)
	start := time.Now()

	_, err := dev.exec(request{opcode: opCtrlGetInfo, dataLen: 8})

	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("exec did not return near its limit")
	}
	if _, err := dev.exec(request{opcode: opCtrlGetInfo, dataLen: 8}); !errors.Is(err, errDeviceBroken) {
		t.Fatalf("a device that timed out must refuse the next call, got %v", err)
	}
	close(release)
}

func TestTimedOutBufferIsNotReleasedWhileTheKernelMayWriteIt(t *testing.T) {
	release := make(chan struct{})
	k := &fakeKernel{written: make(chan struct{}), answer: func(uint32, []byte) (uint8, []byte, error) {
		<-release
		return 0, []byte("late write"), nil
	}}
	dev := newTestDevice(k, 30*time.Millisecond)

	_, _ = dev.exec(request{opcode: opCtrlGetInfo, dataLen: 64})
	runtime.GC()
	close(release)
	<-k.written
	runtime.GC()

	if !bytes.HasPrefix(k.bufs[0], []byte("late write")) {
		t.Fatalf("the late write did not land in a live buffer: %q", k.bufs[0][:16])
	}
}

func TestBuffersStayValidAcrossGarbageCollection(t *testing.T) {
	k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) {
		runtime.GC()
		runtime.GC()
		return 0, bytes.Repeat([]byte{0xAB}, 4096), nil
	}}

	resp, err := newTestDevice(k, time.Second).exec(request{opcode: opCtrlGetInfo, dataLen: 4096})

	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp.data, bytes.Repeat([]byte{0xAB}, 4096)) {
		t.Fatal("the data changed under garbage collection")
	}
}

func TestExecIsSerial(t *testing.T) {
	inflight, peak := 0, 0
	k := &fakeKernel{answer: func(uint32, []byte) (uint8, []byte, error) {
		inflight++
		peak = max(peak, inflight)
		time.Sleep(5 * time.Millisecond)
		inflight--
		return 0, nil, nil
	}}
	dev := newTestDevice(k, time.Second)
	done := make(chan struct{})

	for i := 0; i < 4; i++ {
		go func() {
			_, _ = dev.exec(request{opcode: opCtrlGetInfo, dataLen: 8})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}

	if peak != 1 {
		t.Fatalf("peak concurrent calls = %d, want 1", peak)
	}
}
