// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

// The head of zfs_cmd_t for a path length of 4096 (Linux) and of 1024 (FreeBSD), as a C compiler lays it out.
type zcHead4096 struct {
	Name      [4096]byte
	Src       uint64
	SrcSize   uint64
	Dst       uint64
	DstSize   uint64
	DstFilled int32
	Pad2      int32
	History   uint64
	Value     [2 * 4096]byte
	String    [256]byte
	GUID      uint64
	Conf      uint64
	ConfSize  uint64
	Cookie    uint64
}

type zcHead1024 struct {
	Name      [1024]byte
	Src       uint64
	SrcSize   uint64
	Dst       uint64
	DstSize   uint64
	DstFilled int32
	Pad2      int32
	History   uint64
	Value     [2 * 1024]byte
	String    [256]byte
	GUID      uint64
	Conf      uint64
	ConfSize  uint64
	Cookie    uint64
}

func TestTheOffsetsFollowThePathLengthOfTheSystem(t *testing.T) {
	var linux zcHead4096
	var freebsd zcHead1024

	dst, size, cookie := zcOffsets(4096)
	assert.Equal(t, int(unsafe.Offsetof(linux.Dst)), dst)
	assert.Equal(t, int(unsafe.Offsetof(linux.DstSize)), size)
	assert.Equal(t, int(unsafe.Offsetof(linux.Cookie)), cookie)

	dst, size, cookie = zcOffsets(1024)
	assert.Equal(t, int(unsafe.Offsetof(freebsd.Dst)), dst)
	assert.Equal(t, int(unsafe.Offsetof(freebsd.DstSize)), size)
	assert.Equal(t, int(unsafe.Offsetof(freebsd.Cookie)), cookie)
}

func TestThePinnedOffsetsOfBothSystems(t *testing.T) {
	dst, size, cookie := zcOffsets(4096)
	assert.Equal(t, []int{4112, 4120, 12616}, []int{dst, size, cookie})

	dst, size, cookie = zcOffsets(1024)
	assert.Equal(t, []int{1040, 1048, 3400}, []int{dst, size, cookie})
}

func TestResetZeroesTheWholeBlock(t *testing.T) {
	z := newZcmd()
	assert.NoError(t, z.setName("tank"))
	z.b[zcCookieOff] = 7

	z.reset()

	assert.Equal(t, make([]byte, zcmdSize), z.b)
	assert.Equal(t, "", z.name())
}

func TestTheOffsetsInUseAreTheOnesOfThisSystem(t *testing.T) {
	dst, size, cookie := zcOffsets(zcNameLen)

	assert.Equal(t, []int{dst, size, cookie}, []int{zcDstOff, zcDstSizeOff, zcCookieOff})
}
