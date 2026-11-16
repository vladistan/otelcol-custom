// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"strings"
)

// decodeOID reads the native-endian int32 words the kernel returns for an OID; a trailing partial word is dropped.
func decodeOID(buf []byte) []int32 {
	oid := make([]int32, len(buf)/4)
	for i := range oid {
		oid[i] = int32(binary.NativeEndian.Uint32(buf[i*4:]))
	}
	return oid
}

// trimOIDName cuts the name returned by the OID name call at its first NUL.
func trimOIDName(buf []byte) string {
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}
