// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

import (
	"encoding/binary"
	"slices"
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
		getString: rawString,
		oidType:   oidType,
		bootTime: func() (time.Time, error) {
			tv, err := unix.SysctlTimeval("kern.boottime")
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(int64(tv.Sec), int64(tv.Usec)*1000), nil
		},
		getfsstat: listMounts,
	}
}

// ctlOIDFmt is the meta sysctl that returns the kind and format of an OID.
var ctlOIDFmt = []int32{0, 4}

// rawString reads a sysctl as text with a buffer of its own: a raw kstat node answers a size
// probe with nothing, which makes unix.Sysctl return an empty string.
func rawString(name string) (string, error) {
	oid, err := lookupOID(name)
	if err != nil {
		return "", err
	}
	return readRawString(func(old []byte) (int, error) { return rawSysctl(oid, old, nil) })
}

// oidType returns the sysctl type name of a node, from its kind word.
func oidType(name string) (string, error) {
	oid, err := lookupOID(name)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 64)
	n, err := rawSysctl(append(append([]int32{}, ctlOIDFmt...), oid...), buf, nil)
	if err != nil || n < 4 {
		return "", err
	}
	return kindName(binary.NativeEndian.Uint32(buf)), nil
}

// listMounts returns the mounted file systems from getfsstat, without asking each one to refresh.
func listMounts() ([]mount, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	out := make([]mount, 0, n)
	for i := range buf[:n] {
		st := &buf[i]
		out = append(out, mount{
			from:      cString(st.Mntfromname[:]),
			fsType:    cString(st.Fstypename[:]),
			blockSize: st.Bsize,
			blocks:    st.Blocks,
			free:      st.Bfree,
			avail:     uint64(max(st.Bavail, 0)),
		})
	}
	return out, nil
}

// cString is the text of a NUL-terminated byte array.
func cString(b []byte) string {
	if i := slices.Index(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
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
