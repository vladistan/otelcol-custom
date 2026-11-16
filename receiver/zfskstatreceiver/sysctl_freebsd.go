// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

import (
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// maxOIDLen is CTL_MAXNAME, the longest OID the kernel returns.
	maxOIDLen = 24
	// maxNameLen bounds one sysctl name returned by the OID name call.
	maxNameLen = 1024
)

// Meta sysctls under CTL_SYSCTL (0): name of an OID, next OID, OID of a name.
var (
	ctlName     = []int32{0, 1}
	ctlNext     = []int32{0, 2}
	ctlNameToID = []int32{0, 3}
)

func realSysctl() sysctlFns {
	return sysctlFns{
		lookupOID: lookupOID,
		nextOID:   nextOID,
		oidName:   oidName,
		getUint64: func(name string) (uint64, error) { return unix.SysctlUint64(name) },
		getString: unix.Sysctl,
		bootTime: func() (time.Time, error) {
			tv, err := unix.SysctlTimeval("kern.boottime")
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(int64(tv.Sec), int64(tv.Usec)*1000), nil
		},
	}
}

// rawSysctl calls __sysctl with mib; it returns the number of bytes written to old.
func rawSysctl(mib []int32, old, newp []byte) (int, error) {
	var oldp, newPtr unsafe.Pointer
	oldlen := uintptr(len(old))
	if len(old) > 0 {
		oldp = unsafe.Pointer(&old[0])
	}
	if len(newp) > 0 {
		newPtr = unsafe.Pointer(&newp[0])
	}
	_, _, errno := unix.Syscall6(unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(oldp), uintptr(unsafe.Pointer(&oldlen)),
		uintptr(newPtr), uintptr(len(newp)))
	if errno != 0 {
		return 0, errno
	}
	return int(oldlen), nil
}

func lookupOID(name string) ([]int32, error) {
	buf := make([]byte, maxOIDLen*4)
	n, err := rawSysctl(ctlNameToID, buf, []byte(name))
	if err != nil {
		return nil, err
	}
	return decodeOID(buf[:n]), nil
}

func nextOID(oid []int32) ([]int32, error) {
	mib := append(append([]int32{}, ctlNext...), oid...)
	buf := make([]byte, maxOIDLen*4)
	n, err := rawSysctl(mib, buf, nil)
	if err != nil {
		return nil, err
	}
	return decodeOID(buf[:n]), nil
}

func oidName(oid []int32) (string, error) {
	mib := append(append([]int32{}, ctlName...), oid...)
	buf := make([]byte, maxNameLen)
	n, err := rawSysctl(mib, buf, nil)
	if err != nil {
		return "", err
	}
	return trimOIDName(buf[:n]), nil
}
