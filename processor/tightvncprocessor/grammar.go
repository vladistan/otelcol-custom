// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"strings"
	"time"
)

// stampLen is the width of "YYYY-MM-DD HH:MM:SS:mmm"; the log has a colon, not a dot, before the milliseconds.
const stampLen = len("2006-01-02 15:04:05:000")

// line is one parsed log line: "[ pid/ tid] stamp [:] [marker] message".
type line struct {
	pid, tid int64
	stamp    string
	marker   string
	message  string
}

// parseLine is hand-written because almost every line is dropped as noise and a regexp costs
// several times as much per line.
func parseLine(s string) (ln line, ok bool) {
	i := skipBlanks(s, 0)
	if i == len(s) || s[i] != '[' {
		return ln, false
	}
	if ln.pid, i, ok = scanInt(s, skipBlanks(s, i+1)); !ok {
		return ln, false
	}
	if i = skipBlanks(s, i); i == len(s) || s[i] != '/' {
		return ln, false
	}
	if ln.tid, i, ok = scanInt(s, skipBlanks(s, i+1)); !ok {
		return ln, false
	}
	if i = skipBlanks(s, i); i == len(s) || s[i] != ']' {
		return ln, false
	}
	start := skipBlanks(s, i+1)
	if start == i+1 || len(s)-start < stampLen || !isValidStamp(s[start:start+stampLen]) {
		return ln, false
	}
	ln.stamp = s[start : start+stampLen]

	end := start + stampLen
	rest := skipBlanks(s, end)
	if rest == end {
		return ln, false
	}
	if rest < len(s) && s[rest] == ':' {
		rest = skipBlanks(s, rest+1)
	}
	if rest+1 < len(s) && isMarker(s[rest]) && isBlank(s[rest+1]) {
		ln.marker = s[rest : rest+1]
		rest = skipBlanks(s, rest+1)
	}
	ln.message = strings.TrimRight(s[rest:], " \t\r\n")
	return ln, true
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func isMarker(c byte) bool { return c == '+' || c == '-' || c == '!' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func skipBlanks(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

// scanInt stops at 18 digits so the value cannot overflow int64.
func scanInt(s string, i int) (n int64, next int, ok bool) {
	start := i
	for i < len(s) && isDigit(s[i]) && i-start < 18 {
		n = n*10 + int64(s[i]-'0')
		i++
	}
	return n, i, i > start
}

func isValidStamp(s string) bool {
	const shape = "dddd-dd-dd dd:dd:dd:ddd"
	for i := 0; i < len(shape); i++ {
		if shape[i] == 'd' {
			if !isDigit(s[i]) {
				return false
			}
		} else if s[i] != shape[i] {
			return false
		}
	}
	return true
}

func parseDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// logTime reads the stamp in loc. ok is false when that wall-clock time does not exist there.
func (ln line) logTime(loc *time.Location) (t time.Time, ok bool) {
	year, month, day := parseDigits(ln.stamp[0:4]), parseDigits(ln.stamp[5:7]), parseDigits(ln.stamp[8:10])
	hour, minute, second := parseDigits(ln.stamp[11:13]), parseDigits(ln.stamp[14:16]), parseDigits(ln.stamp[17:19])
	nanos := parseDigits(ln.stamp[20:23]) * int(time.Millisecond)

	t = time.Date(year, time.Month(month), day, hour, minute, second, nanos, loc)
	// Date rolls an impossible time forward (02:30 in a spring-forward gap, month 13) instead of failing.
	if !isSameWallClock(t, year, month, day, hour, minute, second) {
		return time.Time{}, false
	}
	// In a fall-back hour Date may return either occurrence; the log writes the first one first.
	for _, shift := range [...]time.Duration{2 * time.Hour, time.Hour, 30 * time.Minute} {
		if earlier := t.Add(-shift); isSameWallClock(earlier, year, month, day, hour, minute, second) {
			return earlier, true
		}
	}
	return t, true
}

func isSameWallClock(t time.Time, year, month, day, hour, minute, second int) bool {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	return y == year && int(mo) == month && d == day && h == hour && mi == minute && s == second
}
