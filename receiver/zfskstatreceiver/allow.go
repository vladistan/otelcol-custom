// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Request numbers of the ZFS control device, counted from its first request.
// They are the read requests of OpenZFS 2.1 and later; nothing else is sent.
const (
	vecPoolStats       uint = 0x05 // pool configuration and vdev statistics
	vecObjsetStats     uint = 0x12 // properties of one dataset
	vecDatasetListNext uint = 0x14 // next child dataset of a parent
	vecPoolGetProps    uint = 0x27 // pool properties
)

var allowedVecs = map[uint]string{
	vecPoolStats:       "pool stats",
	vecObjsetStats:     "objset stats",
	vecDatasetListNext: "dataset list next",
	vecPoolGetProps:    "pool get props",
}

var errIoctlRefused = errors.New("ioctl request is not on the allow-list")

// checkVec is the one check every request passes before it reaches the device.
func checkVec(vec uint) error {
	if _, ok := allowedVecs[vec]; !ok {
		return fmt.Errorf("%w: %#x", errIoctlRefused, vec)
	}
	return nil
}

// zcmdSize is above sizeof(zfs_cmd_t) on every system: about 13.8 KiB on Linux and less on FreeBSD.
const zcmdSize = 16 * 1024

// zcOffsets returns the offsets of zc_nvlist_dst, zc_nvlist_dst_size and zc_cookie in zfs_cmd_t for a
// path length (MAXPATHLEN), which the kernel struct embeds three times: 4096 on Linux, 1024 on FreeBSD.
func zcOffsets(pathLen int) (dst, dstSize, cookie int) {
	// zc_name, zc_nvlist_src and its size come first, then zc_nvlist_dst and its size.
	dst = pathLen + 16
	dstSize = dst + 8
	// zc_nvlist_dst_filled, a pad, zc_history, zc_value (two paths), zc_string (256), then
	// zc_guid, zc_nvlist_conf and its size lead to zc_cookie.
	cookie = dst + 8 + 8 + 8 + 8 + 2*pathLen + 256 + 24
	return dst, dstSize, cookie
}

// Offsets of the fields the receiver reads and writes in the request block.
var zcDstOff, zcDstSizeOff, zcCookieOff = zcOffsets(zcNameLen)

type zcmd struct{ b []byte }

func newZcmd() *zcmd { return &zcmd{b: make([]byte, zcmdSize)} }

// reset zeroes the block before each call.
func (z *zcmd) reset() { clear(z.b) }

func (z *zcmd) setName(name string) error {
	if len(name) >= zcNameLen {
		return fmt.Errorf("name of %d bytes does not fit", len(name))
	}
	clear(z.b[:zcNameLen])
	copy(z.b, name)
	return nil
}

func (z *zcmd) name() string {
	name := string(z.b[:zcNameLen])
	if i := strings.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return name
}

func (z *zcmd) setDst(addr, size uint64) {
	binary.NativeEndian.PutUint64(z.b[zcDstOff:], addr)
	binary.NativeEndian.PutUint64(z.b[zcDstSizeOff:], size)
}

func (z *zcmd) dstSize() uint64 { return binary.NativeEndian.Uint64(z.b[zcDstSizeOff:]) }

func (z *zcmd) setCookie(c uint64) { binary.NativeEndian.PutUint64(z.b[zcCookieOff:], c) }

func (z *zcmd) cookie() uint64 { return binary.NativeEndian.Uint64(z.b[zcCookieOff:]) }
