// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package strip removes ANSI codes, wrapper prefixes and text escapes from log lines.
package strip

import (
	"regexp"
	"strings"
	"unicode"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b`)

// ANSI removes ANSI colour and cursor sequences, and any lone escape byte.
func ANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansi.ReplaceAllString(s, "")
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Unescape turns the text escapes \r \n \t \\ and \xNN into their characters.
// Consecutive \xNN bytes form UTF-8. Anything it does not know stays as written.
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		switch s[i+1] {
		case 'r':
			out, i = append(out, '\r'), i+1
		case 'n':
			out, i = append(out, '\n'), i+1
		case 't':
			out, i = append(out, '\t'), i+1
		case '\\':
			out, i = append(out, '\\'), i+1
		case 'x':
			if i+3 < len(s) {
				hi, ok1 := hexVal(s[i+2])
				lo, ok2 := hexVal(s[i+3])
				if ok1 && ok2 {
					out, i = append(out, hi<<4|lo), i+3
					continue
				}
			}
			out = append(out, s[i])
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

// SerialConsole unescapes a GCE serial console line, strips ANSI and trims the space around it.
func SerialConsole(s string) string {
	return strings.TrimSpace(ANSI(Unescape(s)))
}

// IsBlank reports whether s holds only white space, such as a bare CRLF record.
func IsBlank(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsSpace(r) }) < 0
}

// Prefix removes prefix from the start of s when it is there.
func Prefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

// TrimEOL removes the CR and LF characters at the end of s and nothing else.
func TrimEOL(s string) string {
	return strings.TrimRight(s, "\r\n")
}
