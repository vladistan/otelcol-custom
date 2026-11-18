// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

func TestFreeBSDRequestCodesWrapTheRequestBlock(t *testing.T) {
	assert.Equal(t, uintptr(0xc0185a05), requestCode(vecPoolStats))
	assert.Equal(t, uintptr(0xc0185a14), requestCode(vecDatasetListNext))
}

func TestFreeBSDRequestBlockIs24Bytes(t *testing.T) {
	assert.Equal(t, uintptr(24), unsafeSizeofIocparm())
}

func TestFreeBSDOpenOfAMissingNodeFails(t *testing.T) {
	_, err := openZFS("/nonexistent/zfs-node")

	assert.Error(t, err)
}

func unsafeSizeofIocparm() uintptr { return unsafe.Sizeof(iocparm{}) }
