// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"strings"
	"unicode/utf8"
)

// ValidUTF8 returns s with each byte that is not part of a valid UTF-8 sequence
// replaced by U+FFFD. Drive and BMC strings can hold raw bytes that Prometheus
// rejects in a label; the label is kept and the text stays readable. Valid
// text is returned as is, without a copy.
func ValidUTF8(s string) string {
	first := firstInvalid(s)
	if first < 0 {
		return s
	}
	var sb strings.Builder
	// Each invalid byte becomes three bytes, so the rest grows by at most two for every byte.
	sb.Grow(len(s) + 2*(len(s)-first))
	sb.WriteString(s[:first])
	for i := first; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune(utf8.RuneError)
		} else {
			sb.WriteString(s[i : i+size])
		}
		i += size
	}
	return sb.String()
}

// firstInvalid returns the index of the first byte that is not part of a valid
// UTF-8 sequence, or -1.
func firstInvalid(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}
