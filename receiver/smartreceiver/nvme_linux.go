// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// nvmeIoctlAdminCmd is NVME_IOCTL_ADMIN_CMD, _IOWR('N', 0x41, struct nvme_admin_cmd): direction 3,
// 72 bytes, type 'N', number 0x41. x/sys/unix has no name for it.
const nvmeIoctlAdminCmd = 3<<30 | 72<<16 | 'N'<<8 | 0x41

// NVMe status codes that mean the controller does not know the command or field.
const (
	nvmeStatusInvalidOpcode = 0x01
	nvmeStatusInvalidField  = 0x02

	// The More and Do Not Retry bits ride on the status the ioctl returns; the code is what is left.
	nvmeStatusMore       = 0x2000
	nvmeStatusDoNotRetry = 0x4000
)

// nvmeAdminCmd is struct nvme_admin_cmd of <linux/nvme_ioctl.h>, 72 bytes.
type nvmeAdminCmd struct {
	opcode      uint8
	flags       uint8
	rsvd1       uint16
	nsid        uint32
	cdw2        uint32
	cdw3        uint32
	metadata    uint64
	addr        uint64
	metadataLen uint32
	dataLen     uint32
	cdw10       uint32
	cdw11       uint32
	cdw12       uint32
	cdw13       uint32
	cdw14       uint32
	cdw15       uint32
	timeoutMS   uint32
	result      uint32
}

// nvmeTransport sends the two allow-listed admin commands to one controller.
type nvmeTransport struct {
	fd      int
	timeout time.Duration
	ioctl   ioctlFunc
	// inflight is shared by every transport opened for the same device; nil in tests that do not need it.
	inflight *inflight

	// fill, set only by tests, writes the answer into the data buffer before the ioctl returns; the
	// kernel does this in production through the address in the command.
	fill func(data []byte)
}

// Admin implements smartdecode.NVMeTransport. The allow list is checked again here.
func (t *nvmeTransport) Admin(cmd smartdecode.NVMeCommand) ([]byte, error) {
	if err := smartdecode.CheckNVMe(cmd); err != nil {
		return nil, err
	}
	data := make([]byte, cmd.DataLen)
	req := nvmeAdminCmd{
		opcode:    cmd.Opcode,
		nsid:      cmd.NSID,
		cdw10:     cmd.CDW10,
		cdw11:     cmd.CDW11,
		dataLen:   uint32(cmd.DataLen),
		timeoutMS: uint32(t.timeout / time.Millisecond),
	}
	if cmd.DataLen > 0 {
		req.addr = uint64(uintptr(unsafe.Pointer(&data[0]))) //nolint:gosec // the kernel reads the buffer address from the struct; data stays alive until KeepAlive
	}
	if t.fill != nil {
		t.fill(data)
	}
	if !t.inflight.acquire() {
		return nil, errStillRunning()
	}

	type outcome struct {
		status int
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		status, err := t.ioctl(t.fd, nvmeIoctlAdminCmd, unsafe.Pointer(&req))
		// The kernel holds the buffer address as a number, so only this reference keeps data alive. The
		// goroutine owns it until the ioctl returns, also after the caller gave up on a timeout.
		runtime.KeepAlive(data)
		t.inflight.release()
		done <- outcome{status, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			return nil, mapErrno(o.err)
		}
		switch code := o.status &^ (nvmeStatusMore | nvmeStatusDoNotRetry); code {
		case 0:
			return data, nil
		case nvmeStatusInvalidOpcode, nvmeStatusInvalidField:
			return nil, fmt.Errorf("%w: NVMe status %#x", smartdecode.ErrUnsupported, o.status)
		default:
			return nil, fmt.Errorf("NVMe admin command failed with status %#x", o.status)
		}
	case <-time.After(t.timeout + timeoutGuard):
		return nil, fmt.Errorf("%w: the NVMe admin ioctl did not return", smartdecode.ErrTimeout)
	}
}
