// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package ethtoolreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ethGstringLen = 32
	ethSSStats    = 1 // ETH_SS_STATS, the statistics string set
	// statHeadroom is spare room in the string and value buffers. The kernel
	// writes its own current count, which can exceed the one asked for after a
	// reset, so a buffer sized to the request alone could be overrun.
	statHeadroom = 256

	// Offsets in the command blocks the kernel defines (struct ethtool_sset_info,
	// ethtool_gstrings and ethtool_stats); every field is in native byte order.
	ssetInfoSize   = 24 // cmd, reserved, 64-bit mask, one count
	ssetMaskOffset = 8
	ssetDataOffset = 16
	stringsHeader  = 12 // cmd, string_set, len
	stringsLenOff  = 8
	statsHeader    = 8 // cmd, n_stats
	statsLenOff    = 4
)

// native is the byte order of the host, which the ethtool blocks use.
var native = binary.NativeEndian

// ifreq is struct ifreq with the data pointer in the union; the padding makes
// it at least as large as the kernel's, which the kernel copies in full.
type ifreq struct {
	name [unix.IFNAMSIZ]byte
	data unsafe.Pointer
	_    [24 - unsafe.Sizeof(uintptr(0))]byte
}

// linuxNIC issues the ethtool ioctls; tests replace the ioctl and list functions.
type linuxNIC struct {
	ioctl  func(*ifreq) error
	list   func() ([]string, error)
	closer func() error
}

// newNIC opens the datagram socket the ethtool ioctls are issued on.
func newNIC() (nicReader, error) {
	var fd int
	var err error
	for _, family := range []int{unix.AF_INET, unix.AF_INET6, unix.AF_NETLINK} {
		if fd, err = unix.Socket(family, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0); err == nil {
			break
		}
		if family == unix.AF_NETLINK {
			// A netlink socket takes a raw type, not a datagram one.
			if fd, err = unix.Socket(family, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_GENERIC); err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open a socket for the ethtool ioctl: %w", err)
	}
	return &linuxNIC{
		ioctl:  func(ifr *ifreq) error { return sysIoctl(fd, ifr) },
		list:   interfaceNames,
		closer: func() error { return unix.Close(fd) },
	}, nil
}

// sysIoctl issues SIOCETHTOOL on fd, retrying when a signal interrupts it.
func sysIoctl(fd int, ifr *ifreq) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.SIOCETHTOOL, uintptr(unsafe.Pointer(ifr)))
		switch errno {
		case 0:
			return nil
		case unix.EINTR:
			continue
		default:
			return errno
		}
	}
}

// interfaceNames lists the interfaces of the network namespace.
func interfaceNames() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		names = append(names, i.Name)
	}
	return names, nil
}

// interfaces lists the interface names the receiver may read.
func (n *linuxNIC) interfaces() ([]string, error) { return n.list() }

// bootTime derives the start of the host from its uptime.
func (n *linuxNIC) bootTime() (time.Time, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return time.Time{}, fmt.Errorf("read the host uptime: %w", err)
	}
	return time.Now().Add(-time.Duration(info.Uptime) * time.Second), nil
}

// close closes the socket, if there is one.
func (n *linuxNIC) close() error {
	if n.closer == nil {
		return nil
	}
	return n.closer()
}

// call runs one SIOCETHTOOL request whose command block starts at data.
func (n *linuxNIC) call(iface string, data unsafe.Pointer) error {
	if len(iface) >= unix.IFNAMSIZ {
		return fmt.Errorf("interface name %q is longer than %d bytes", iface, unix.IFNAMSIZ-1)
	}
	ifr := ifreq{data: data}
	copy(ifr.name[:], iface)
	err := n.ioctl(&ifr)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOTTY):
		return errNotSupported
	case errors.Is(err, unix.ENODEV):
		return errNoDevice
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		return errPermission
	default:
		return fmt.Errorf("ethtool ioctl on %s: %w", iface, err)
	}
}

// driver issues ETHTOOL_GDRVINFO and returns the driver name.
func (n *linuxNIC) driver(iface string) (string, error) {
	info := unix.EthtoolDrvinfo{Cmd: unix.ETHTOOL_GDRVINFO}
	if err := n.call(iface, unsafe.Pointer(&info)); err != nil {
		return "", err
	}
	return trimNUL(info.Driver[:]), nil
}

// statCount issues ETHTOOL_GSSET_INFO for the statistics string set.
func (n *linuxNIC) statCount(iface string) (int, error) {
	buf := make([]byte, ssetInfoSize)
	native.PutUint32(buf[0:], unix.ETHTOOL_GSSET_INFO)
	native.PutUint64(buf[ssetMaskOffset:], 1<<ethSSStats)
	if err := n.call(iface, unsafe.Pointer(&buf[0])); err != nil {
		return 0, err
	}
	if native.Uint64(buf[ssetMaskOffset:])&(1<<ethSSStats) == 0 {
		return 0, errNotSupported
	}
	return int(native.Uint32(buf[ssetDataOffset:])), nil
}

// statNames issues ETHTOOL_GSTRINGS and returns the names the kernel reports.
func (n *linuxNIC) statNames(iface string, count int) ([]string, error) {
	capacity := count + statHeadroom
	buf := make([]byte, stringsHeader+capacity*ethGstringLen)
	native.PutUint32(buf[0:], unix.ETHTOOL_GSTRINGS)
	native.PutUint32(buf[4:], ethSSStats)
	native.PutUint32(buf[stringsLenOff:], uint32(count)) //nolint:gosec // a statistic count is small
	if err := n.call(iface, unsafe.Pointer(&buf[0])); err != nil {
		return nil, err
	}
	got := int(native.Uint32(buf[stringsLenOff:]))
	if got > capacity {
		return nil, errCountChanged
	}
	names := make([]string, got)
	for i := range names {
		names[i] = trimNUL(buf[stringsHeader+i*ethGstringLen : stringsHeader+(i+1)*ethGstringLen])
	}
	return names, nil
}

// statValues issues ETHTOOL_GSTATS and returns the values the kernel reports.
func (n *linuxNIC) statValues(iface string, count int) ([]uint64, error) {
	capacity := count + statHeadroom
	buf := make([]byte, statsHeader+capacity*8)
	native.PutUint32(buf[0:], unix.ETHTOOL_GSTATS)
	native.PutUint32(buf[statsLenOff:], uint32(count)) //nolint:gosec // a statistic count is small
	if err := n.call(iface, unsafe.Pointer(&buf[0])); err != nil {
		return nil, err
	}
	got := int(native.Uint32(buf[statsLenOff:]))
	if got > capacity {
		return nil, errCountChanged
	}
	values := make([]uint64, got)
	for i := range values {
		values[i] = native.Uint64(buf[statsHeader+i*8:])
	}
	return values, nil
}

// trimNUL cuts a fixed-size C string at its first NUL byte.
func trimNUL(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
