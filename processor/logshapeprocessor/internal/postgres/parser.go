// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"regexp"
	"strings"
	"time"
)

// PostgresLogInfo holds parsed information from a PostgreSQL log entry.
type PostgresLogInfo struct {
	Timestamp string // The timestamp as written, without the zone
	Zone      string // The zone name of the timestamp (UTC, CET)
	Level     string // LOG, ERROR, WARNING, NOTICE, INFO, DEBUG, FATAL, PANIC
	PID       string // Process ID
	Message   string // The main log message
	Detail    string // DETAIL line if present
	Statement string // STATEMENT line if present
	Hint      string // HINT line if present
	Context   string // CONTEXT line if present
}

// PostgreSQL log format: 2025-12-31 00:01:39.618 UTC [1975] ERROR:  message
// Also handles continuation lines: DETAIL:, STATEMENT:, HINT:, CONTEXT:
var postgresLogPattern = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+` + // timestamp
		`(\w+)\s+` + // timezone (UTC, etc.)
		`\[(\d+)\]\s+` + // [pid]
		`(LOG|ERROR|WARNING|NOTICE|INFO|DEBUG\d?|FATAL|PANIC):\s+` + // level:
		`(.*)$`, // message
)

// Continuation line patterns
var detailPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+\w+\s+\[\d+\]\s+DETAIL:\s+(.*)$`)
var statementPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+\w+\s+\[\d+\]\s+STATEMENT:\s+(.*)$`)
var hintPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+\w+\s+\[\d+\]\s+HINT:\s+(.*)$`)
var contextPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+\w+\s+\[\d+\]\s+CONTEXT:\s+(.*)$`)

// IsPostgresLog checks if a log line looks like a PostgreSQL log entry.
func IsPostgresLog(body string) bool {
	if len(body) < 30 {
		return false
	}

	// Quick check: must start with a timestamp (YYYY-MM-DD)
	if len(body) > 10 && body[4] == '-' && body[7] == '-' && body[10] == ' ' {
		// Check for typical PostgreSQL format with [pid] and level
		return strings.Contains(body, "] LOG:") ||
			strings.Contains(body, "] ERROR:") ||
			strings.Contains(body, "] WARNING:") ||
			strings.Contains(body, "] NOTICE:") ||
			strings.Contains(body, "] INFO:") ||
			strings.Contains(body, "] DEBUG") ||
			strings.Contains(body, "] FATAL:") ||
			strings.Contains(body, "] PANIC:") ||
			strings.Contains(body, "] DETAIL:") ||
			strings.Contains(body, "] STATEMENT:") ||
			strings.Contains(body, "] HINT:") ||
			strings.Contains(body, "] CONTEXT:")
	}

	return false
}

// ParsePostgresLog parses a PostgreSQL log entry and extracts structured information.
// It handles multi-line logs that contain DETAIL, STATEMENT, HINT, and CONTEXT.
func ParsePostgresLog(body string) *PostgresLogInfo {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return nil
	}

	// Parse the main log line
	mainLine := lines[0]
	matches := postgresLogPattern.FindStringSubmatch(mainLine)
	if len(matches) != 6 {
		// Check if it's a continuation line (DETAIL, STATEMENT, etc.)
		if detailPattern.MatchString(mainLine) ||
			statementPattern.MatchString(mainLine) ||
			hintPattern.MatchString(mainLine) ||
			contextPattern.MatchString(mainLine) {
			// This is a continuation line without a main message
			// Return nil as we don't want to process these standalone
			return nil
		}
		return nil
	}

	info := &PostgresLogInfo{
		Timestamp: matches[1],
		Zone:      matches[2],
		Level:     normalizeLevel(matches[4]),
		PID:       matches[3],
		Message:   strings.TrimSpace(matches[5]),
	}

	// Parse continuation lines
	for i := 1; i < len(lines); i++ {
		line := lines[i]

		if m := detailPattern.FindStringSubmatch(line); len(m) == 3 {
			info.Detail = strings.TrimSpace(m[2])
		} else if m := statementPattern.FindStringSubmatch(line); len(m) == 3 {
			info.Statement = strings.TrimSpace(m[2])
		} else if m := hintPattern.FindStringSubmatch(line); len(m) == 3 {
			info.Hint = strings.TrimSpace(m[2])
		} else if m := contextPattern.FindStringSubmatch(line); len(m) == 3 {
			info.Context = strings.TrimSpace(m[2])
		}
	}

	return info
}

// normalizeLevel normalizes PostgreSQL log levels.
// DEBUG1, DEBUG2, etc. become DEBUG.
func normalizeLevel(level string) string {
	if strings.HasPrefix(level, "DEBUG") {
		return "DEBUG"
	}
	return level
}

var zoneOffsets = map[string]int{"UTC": 0, "GMT": 0, "CET": 1, "CEST": 2}

// LineTime reads the timestamp of a log line in the zone the line names. Only UTC, GMT, CET and CEST are
// known; any other zone is not guessed and ok is false.
func LineTime(timestamp, zone string) (time.Time, bool) {
	hours, known := zoneOffsets[zone]
	if !known {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05.999999999", timestamp)
	if err != nil {
		return time.Time{}, false
	}
	return t.Add(-time.Duration(hours) * time.Hour), true
}

// BuildCleanedMessage returns the message text. When a constraint is violated it appends the detail line.
func BuildCleanedMessage(info *PostgresLogInfo) string {
	var sb strings.Builder
	sb.WriteString(info.Message)

	// For constraint violations, append the key detail
	if info.Detail != "" && strings.Contains(info.Message, "constraint") {
		sb.WriteString(" (")
		sb.WriteString(info.Detail)
		sb.WriteString(")")
	}

	return sb.String()
}
