// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"strings"
	"unicode/utf8"
)

const maxValueLen = 128

// placeholders are the filler strings firmware writes where it has no value.
var placeholders = map[string]bool{
	"to be filled by o.e.m.": true, "to be filled by oem": true, "default string": true, "not specified": true,
	"not applicable": true, "not available": true, "n/a": true, "na": true, "none": true, "unknown": true,
	"undefined": true, "unspecified": true, "o.e.m.": true, "oem": true, "empty": true, "default": true,
	"system product name": true, "system manufacturer": true, "system version": true, "system serial number": true,
	"base board serial number": true, "base board product name": true, "base board manufacturer": true,
	"base board version": true, "chassis serial number": true, "chassis manufacturer": true, "chassis version": true,
	"serial number": true, "no dimm": true, "no module installed": true, "not installed": true,
	"type1productconfigid": true, "123456789": true, "0123456789": true, "0123456789abcdef": true,
}

// cleanValue returns the text of a firmware string without control characters
// or invalid bytes, or "" when it is empty, too fill-like to be data, or a placeholder.
func cleanValue(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxValueLen {
		s = s[:maxValueLen]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	if s == "" || placeholders[strings.ToLower(s)] || strings.HasPrefix(strings.ToLower(s), "to be filled") || sameFill(s) {
		return ""
	}
	return s
}

// sameFill reports a string made of one repeated fill character, such as 0000 or XXXX.
func sameFill(s string) bool {
	if !strings.ContainsRune("0fFxX-._*?#", rune(s[0])) {
		return false
	}
	return strings.Trim(s, s[:1]) == ""
}
