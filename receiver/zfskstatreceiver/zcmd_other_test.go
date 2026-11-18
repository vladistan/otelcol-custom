// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !freebsd

package zfskstatreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathLengthOutsideFreeBSDIs4096(t *testing.T) {
	assert.Equal(t, 4096, zcNameLen)
}
