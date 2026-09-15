// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sentrylogprocessor

import (
	"regexp"
	"strings"
	"time"
)

// SentryLogInfo contains parsed information from a Sentry log line.
type SentryLogInfo struct {
	// Timestamp from the log line (time only, no date)
	Timestamp time.Time

	// Level is the log level (INFO, DEBUG, WARNING, ERROR, CRITICAL)
	Level string

	// Logger is the logger name (e.g., sentry.access.api)
	Logger string

	// Message is the log message category (e.g., api.access)
	MessageType string

	// Message is the cleaned log message
	Message string

	// HTTP request fields (when present)
	Method        string
	View          string
	Response      string
	Path          string
	CallerIP      string
	UserAgent     string
	Duration      string
	RateLimited   string
	IsFrontend    string
	RateLimitType string

	// Raw key-value pairs from the log
	Fields map[string]string
}

// Main log pattern: HH:MM:SS [LEVEL] logger.name: message_type (key='value' ...)
// Example: 02:30:21 [INFO] sentry.access.api: api.access (method='GET' view='...' ...)
var mainPattern = regexp.MustCompile(
	`^(\d{2}:\d{2}:\d{2})\s+\[(\w+)\]\s+([^:]+):\s+(\S+)\s*\((.+)\)$`,
)

// Simple log pattern without key-value pairs
// Example: 02:30:21 [INFO] logger.name: some message
var simplePattern = regexp.MustCompile(
	`^(\d{2}:\d{2}:\d{2})\s+\[(\w+)\]\s+([^:]+):\s+(.+)$`,
)

// Key-value pattern for extracting fields
var kvPattern = regexp.MustCompile(`(\w+)='([^']*)'`)

// IsSentryLog checks if a log line appears to be from Sentry.
func IsSentryLog(line string) bool {
	// Quick check: must start with time format HH:MM:SS and contain [LEVEL]
	if len(line) < 15 {
		return false
	}
	// Check for time format at start
	if line[2] != ':' || line[5] != ':' {
		return false
	}
	// Check for [LEVEL] bracket
	return strings.Contains(line[:30], "[")
}

// ParseSentryLog parses a Sentry log line and returns structured info.
func ParseSentryLog(line string) *SentryLogInfo {
	// Try main pattern with key-value pairs first
	matches := mainPattern.FindStringSubmatch(line)
	if matches != nil {
		info := &SentryLogInfo{
			Level:       matches[2],
			Logger:      matches[3],
			MessageType: matches[4],
			Fields:      make(map[string]string),
		}

		// Parse timestamp (time only)
		if ts, err := time.Parse("15:04:05", matches[1]); err == nil {
			info.Timestamp = ts
		}

		// Extract key-value pairs
		kvMatches := kvPattern.FindAllStringSubmatch(matches[5], -1)
		for _, kv := range kvMatches {
			key := kv[1]
			value := kv[2]
			info.Fields[key] = value

			// Map to specific fields
			switch key {
			case "method":
				info.Method = value
			case "view":
				info.View = value
			case "response":
				info.Response = value
			case "path":
				info.Path = value
			case "caller_ip":
				info.CallerIP = value
			case "user_agent":
				info.UserAgent = value
			case "request_duration_seconds":
				info.Duration = value
			case "rate_limited":
				info.RateLimited = value
			case "is_frontend_request":
				info.IsFrontend = value
			case "rate_limit_type":
				info.RateLimitType = value
			}
		}

		// Build cleaned message
		info.Message = buildCleanedMessage(info)

		return info
	}

	// Try simple pattern
	matches = simplePattern.FindStringSubmatch(line)
	if matches != nil {
		info := &SentryLogInfo{
			Level:   matches[2],
			Logger:  matches[3],
			Message: matches[4],
			Fields:  make(map[string]string),
		}

		// Parse timestamp (time only)
		if ts, err := time.Parse("15:04:05", matches[1]); err == nil {
			info.Timestamp = ts
		}

		return info
	}

	return nil
}

// buildCleanedMessage creates a human-readable message from parsed fields.
// Format: {status_code} {method} {path}
func buildCleanedMessage(info *SentryLogInfo) string {
	if info.Response != "" && info.Method != "" && info.Path != "" {
		// HTTP access log format: 302 GET /
		return info.Response + " " + info.Method + " " + info.Path
	}

	// Fallback to message type
	return info.MessageType
}

// SeverityFromLevel converts Sentry log level to OTEL severity.
// Returns (severity_number, severity_text).
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 5, "DEBUG" // SEVERITY_NUMBER_DEBUG
	case "INFO":
		return 9, "INFO" // SEVERITY_NUMBER_INFO
	case "WARNING", "WARN":
		return 13, "WARN" // SEVERITY_NUMBER_WARN
	case "ERROR":
		return 17, "ERROR" // SEVERITY_NUMBER_ERROR
	case "CRITICAL", "FATAL":
		return 21, "FATAL" // SEVERITY_NUMBER_FATAL
	default:
		return 9, "INFO"
	}
}
