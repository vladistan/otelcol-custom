// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package multiline folds the lines of one stack trace into one record.
package multiline

import (
	"regexp"
	"strings"
)

const defaultMaxBytes = 1 << 16

var (
	exceptionLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*(: .*)?$`)
	moreLine      = regexp.MustCompile(`^\.\.\. \d+ more$`)
)

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// IsStart reports whether line opens a stack trace.
func IsStart(line string) bool {
	return strings.HasPrefix(line, "Traceback (most recent call last):") ||
		strings.HasPrefix(line, "Exception in thread ")
}

func isChain(line string) bool {
	return strings.HasPrefix(line, "During handling of the above exception") ||
		strings.HasPrefix(line, "The above exception was the direct cause")
}

// IsContinuation reports whether line belongs to the record before it.
func IsContinuation(line string) bool {
	if isBlank(line) {
		return false
	}
	return line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "Caused by:") ||
		moreLine.MatchString(line) || isChain(line)
}

// Merger folds lines into records. The zero value is ready; MaxBytes 0 means 64 KiB.
type Merger struct {
	MaxBytes int

	pending      string
	has          bool
	inTrace      bool
	sawException bool
}

func (m *Merger) limit() int {
	if m.MaxBytes > 0 {
		return m.MaxBytes
	}
	return defaultMaxBytes
}

func (m *Merger) take() (string, bool) {
	rec, ok := m.pending, m.has
	m.pending, m.has = "", false
	return rec, ok
}

// Add feeds one line. When it closes the record before it, that record is returned.
func (m *Merger) Add(line string) (string, bool) {
	if isBlank(line) {
		return "", false
	}
	if m.has && m.joins(line) {
		if len(m.pending)+1+len(line) > m.limit() {
			rec, _ := m.take()
			m.pending, m.has = line, true
			return rec, true
		}
		m.pending += "\n" + line
		return "", false
	}
	rec, ok := m.take()
	m.pending, m.has = line, true
	m.inTrace = IsStart(line)
	m.sawException = false
	return rec, ok
}

func (m *Merger) joins(line string) bool {
	if IsStart(line) {
		return false
	}
	if IsContinuation(line) {
		if isChain(line) {
			m.sawException = false
		}
		return true
	}
	if m.inTrace && exceptionLine.MatchString(line) && (!m.sawException || strings.Contains(line, ": ")) {
		m.sawException = true
		return true
	}
	return false
}

// Flush returns the record still pending, if any.
func (m *Merger) Flush() (string, bool) {
	m.inTrace, m.sawException = false, false
	return m.take()
}

// SplitException returns the exception type and message from the last line of a Python traceback.
func SplitException(record string) (typ, msg string, ok bool) {
	first, _, _ := strings.Cut(record, "\n")
	if !IsStart(first) {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(record, "\n"), "\n")
	last := lines[len(lines)-1]
	if !exceptionLine.MatchString(last) {
		return "", "", false
	}
	typ, msg, _ = strings.Cut(last, ": ")
	return typ, msg, true
}

// JoinLines joins the lines of one journald batch into one message.
func JoinLines(lines []string) string { return strings.Join(lines, "\n") }
