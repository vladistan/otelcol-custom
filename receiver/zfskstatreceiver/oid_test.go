// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

func oidBytes(words ...int32) []byte {
	buf := make([]byte, 0, len(words)*4)
	for _, w := range words {
		buf = binary.NativeEndian.AppendUint32(buf, uint32(w))
	}
	return buf
}

func TestDecodeOID(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []int32
	}{
		{"empty", nil, []int32{}},
		{"one word", oidBytes(5), []int32{5}},
		{"kstat leaf", oidBytes(5, 12, 3, 70000), []int32{5, 12, 3, 70000}},
		{"negative word", oidBytes(-1, 2), []int32{-1, 2}},
		{"partial trailing word is dropped", append(oidBytes(1, 2), 0xff, 0xff), []int32{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decodeOID(tt.in))
		})
	}
}

func TestTrimOIDName(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, ""},
		{"no NUL", []byte("kstat.zfs.tank"), "kstat.zfs.tank"},
		{"trailing NUL", []byte("kstat.zfs.tank\x00"), "kstat.zfs.tank"},
		{"NUL then padding", []byte("kstat.zfs\x00\x00\x00junk"), "kstat.zfs"},
		{"only NUL", []byte{0}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, trimOIDName(tt.in))
		})
	}
}
