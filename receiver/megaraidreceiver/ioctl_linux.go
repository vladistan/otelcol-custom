// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package megaraidreceiver

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// nodePath is the character device of the megaraid_sas driver; it is the only device the
// receiver opens. This is the one file with the syscall; it has no unit test and is left out of the coverage figure.
const nodePath = "/dev/megaraid_sas_ioctl_node"

// procDevices lists the major numbers of the character devices; create_node reads it.
const procDevices = "/proc/devices"

const callTimeout = 5 * time.Second

// describeNode reports the owner and mode of the node for the access error message.
func describeNode() string {
	var st syscall.Stat_t
	if err := syscall.Stat(nodePath, &st); err != nil {
		return "unreadable: " + err.Error()
	}
	return fmt.Sprintf("mode %04o uid %d gid %d", st.Mode&0o7777, st.Uid, st.Gid)
}

// openNode opens the ioctl node for one controller. The open needs file access to the node and
// the driver also refuses it without CAP_SYS_ADMIN, whatever the file mode says. With create set and the node missing, it makes the
// node from the major number the driver registered, which needs CAP_MKNOD.
func openNode(hostNo int, create bool) (device, func(), error) {
	fd, err := syscall.Open(nodePath, syscall.O_RDWR, 0)
	if errors.Is(err, syscall.ENOENT) && create {
		if mkErr := makeNode(); mkErr != nil {
			return nil, nil, fmt.Errorf("%w: %s: create_node failed: %v", errNodeMissing, nodePath, mkErr)
		}
		fd, err = syscall.Open(nodePath, syscall.O_RDWR, 0)
	}
	switch {
	case errors.Is(err, syscall.ENOENT):
		return nil, nil, fmt.Errorf("%w: %s", errNodeMissing, nodePath)
	case err != nil:
		return nil, nil, mapOpenError(err, describeNode())
	}
	dev := &ioctlDevice{
		hostNo:  uint16(hostNo), //nolint:gosec // host numbers are small
		timeout: callTimeout,
		ioctl: func(packet []byte, buffers ...[]byte) error {
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ioctlFirmware, uintptr(unsafe.Pointer(&packet[0])))
			runtime.KeepAlive(packet)
			runtime.KeepAlive(buffers)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}
	return dev, func() { _ = syscall.Close(fd) }, nil
}

// makeNode creates the character device node for the megaraid_sas_ioctl major.
func makeNode() error {
	f, err := os.Open(procDevices)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == "megaraid_sas_ioctl" {
			major, err := strconv.Atoi(fields[0])
			if err != nil || major < 0 || major > 255 {
				return fmt.Errorf("unusable major number %q", fields[0])
			}
			return syscall.Mknod(nodePath, syscall.S_IFCHR|0o600, major<<8)
		}
	}
	return errors.New("the driver has not registered megaraid_sas_ioctl in " + procDevices)
}
