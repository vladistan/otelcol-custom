package collect

import (
	"strings"
	"unicode/utf8"
)

// validUTF8 returns s with each byte that is not part of a valid UTF-8
// sequence replaced by U+FFFD. Driver strings can hold raw bytes that
// Prometheus rejects in a label. The module keeps its own copy because it does
// not depend on hwschema; valid text is returned as is.
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
