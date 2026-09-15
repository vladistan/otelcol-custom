// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pgbouncerlogprocessor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PgBouncerLogInfo contains parsed PgBouncer log fields.
type PgBouncerLogInfo struct {
	Timestamp    time.Time
	PID          int
	Level        string // LOG, DEBUG, WARNING, ERROR, FATAL
	ConnectionID string // C-0x... or S-0x...
	Database     string
	User         string
	ClientIP     string
	ClientPort   int
	Message      string
}

// PgBouncer log format examples:
// 2025-12-31 01:46:17.407 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 login attempt: db=postgres user=postgres tls=no replication=no
// 2025-12-31 01:46:17.429 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 closing because: client close request (age=0s)
// 2025-12-31 01:45:00.000 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
// 2025-12-31 01:45:00.000 UTC [1] WARNING pooler error: no such database: test

// Main log pattern: timestamp [pid] LEVEL connection_info: message
// Timestamp format: 2025-12-31 01:46:17.407 UTC
var mainPattern = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+)\s+(\w+)\s+\[(\d+)\]\s+(\w+)\s+(.*)$`,
)

// Connection pattern: C-0x... or S-0x...: db/user@ip:port message
var connectionPattern = regexp.MustCompile(
	`^([CS]-0x[0-9a-fA-F]+):\s+(\w+)/(\w+)@([\d.]+):(\d+)\s+(.*)$`,
)

// Simple connection pattern for messages without db/user info: C-0x...: message
var simpleConnectionPattern = regexp.MustCompile(
	`^([CS]-0x[0-9a-fA-F]+):\s+(.*)$`,
)

// IsPgBouncerLog checks if a string looks like a PgBouncer log line.
func IsPgBouncerLog(s string) bool {
	// Quick check for common PgBouncer patterns
	if len(s) < 30 {
		return false
	}

	// Must start with timestamp pattern: YYYY-MM-DD HH:
	if len(s) > 13 && s[4] == '-' && s[7] == '-' && s[10] == ' ' && s[13] == ':' {
		// Check for [pid] pattern after timestamp
		idx := strings.Index(s, "[")
		if idx > 20 && idx < 40 {
			return true
		}
	}
	return false
}

// ParsePgBouncerLog parses a PgBouncer log line and extracts structured fields.
func ParsePgBouncerLog(line string) *PgBouncerLogInfo {
	matches := mainPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	info := &PgBouncerLogInfo{
		Level:   matches[4],
		Message: matches[5],
	}

	// Parse timestamp: 2025-12-31 01:46:17.407 UTC
	timestampStr := matches[1] + " " + matches[2]
	if t, err := time.Parse("2006-01-02 15:04:05.000 MST", timestampStr); err == nil {
		info.Timestamp = t
	} else if t, err := time.Parse("2006-01-02 15:04:05.000000 MST", timestampStr); err == nil {
		info.Timestamp = t
	}

	// Parse PID
	if pid, err := strconv.Atoi(matches[3]); err == nil {
		info.PID = pid
	}

	// Try to parse connection info from the message
	connMatches := connectionPattern.FindStringSubmatch(info.Message)
	if connMatches != nil {
		info.ConnectionID = connMatches[1]
		info.Database = connMatches[2]
		info.User = connMatches[3]
		info.ClientIP = connMatches[4]
		if port, err := strconv.Atoi(connMatches[5]); err == nil {
			info.ClientPort = port
		}
		info.Message = connMatches[6]
	} else {
		// Try simple connection pattern
		simpleMatches := simpleConnectionPattern.FindStringSubmatch(info.Message)
		if simpleMatches != nil {
			info.ConnectionID = simpleMatches[1]
			info.Message = simpleMatches[2]
		}
	}

	return info
}

// SeverityFromLevel converts PgBouncer log level to OTEL severity.
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 5, "DEBUG"
	case "LOG":
		return 9, "INFO" // LOG in PgBouncer is equivalent to INFO
	case "INFO":
		return 9, "INFO"
	case "NOTICE":
		return 9, "INFO"
	case "WARNING":
		return 13, "WARN"
	case "ERROR":
		return 17, "ERROR"
	case "FATAL":
		return 21, "FATAL"
	case "PANIC":
		return 23, "FATAL"
	default:
		return 9, "INFO"
	}
}
