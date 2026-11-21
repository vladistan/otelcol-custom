// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"strings"
	"unicode/utf8"
)

// validUTF8 returns s with each byte outside a valid UTF-8 sequence replaced by
// U+FFFD. Drives answer identify strings with raw bytes, and Prometheus rejects
// them as label values. The module has no dependency on hwschema, so the same
// rule lives here and in hwschema.ValidUTF8.
func validUTF8(s string) string {
	first := -1
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			first = i
			break
		}
		i += size
	}
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
