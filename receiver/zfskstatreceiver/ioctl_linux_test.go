// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package zfskstatreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinuxRequestCodesAreTheZfsNumbers(t *testing.T) {
	assert.Equal(t, uintptr(0x5a05), requestCode(vecPoolStats))
	assert.Equal(t, uintptr(0x5a12), requestCode(vecObjsetStats))
	assert.Equal(t, uintptr(0x5a14), requestCode(vecDatasetListNext))
	assert.Equal(t, uintptr(0x5a27), requestCode(vecPoolGetProps))
}

func TestLinuxOpenOfAMissingNodeFails(t *testing.T) {
	_, err := openZFS("/nonexistent/zfs-node")

	assert.Error(t, err)
}
