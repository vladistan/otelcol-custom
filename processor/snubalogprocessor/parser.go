// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snubalogprocessor

import (
	"encoding/json"
	"regexp"
	"strings"
)

// RustTracingLog represents a log entry from Rust's tracing crate in JSON format.
// Example: {"timestamp":"2025-12-31T03:16:39.015154Z","level":"INFO","fields":{"message":"Inserted 1 rows"},"target":"rust_snuba::strategies::clickhouse::writer_v2"}
type RustTracingLog struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Fields    map[string]interface{} `json:"fields"`
	Target    string                 `json:"target"`
	Span      map[string]interface{} `json:"span,omitempty"`
}

// SnubaLogInfo holds parsed information from a Snuba log entry.
type SnubaLogInfo struct {
	Message   string
	Level     string
	Target    string
	Storage   string
	IsJSON    bool
	RawFields map[string]interface{}
}

// Python log format: "2025-12-30 21:28:06,251 Initializing Snuba..."
var pythonLogPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2},\d{3}\s+(.+)$`)

// IsSnubaLog checks if a log line looks like a Snuba log entry.
// Matches Rust tracing JSON format or Python logging format.
func IsSnubaLog(body string) bool {
	if len(body) < 10 {
		return false
	}

	// Check for JSON format (Rust tracing)
	if strings.HasPrefix(body, "{") && strings.Contains(body, `"timestamp"`) {
		return true
	}

	// Check for Python logging format
	if pythonLogPattern.MatchString(body) {
		return true
	}

	return false
}

// ParseSnubaLog parses a Snuba log entry and extracts structured information.
func ParseSnubaLog(body string) *SnubaLogInfo {
	// Try JSON format first (Rust tracing)
	if strings.HasPrefix(body, "{") {
		if info := parseRustTracingJSON(body); info != nil {
			return info
		}
	}

	// Try Python logging format
	if matches := pythonLogPattern.FindStringSubmatch(body); len(matches) == 2 {
		return &SnubaLogInfo{
			Message: matches[1],
			Level:   "INFO", // Python logs in Snuba don't include level in this format
			IsJSON:  false,
		}
	}

	return nil
}

// parseRustTracingJSON parses a JSON log line from Rust's tracing crate.
func parseRustTracingJSON(body string) *SnubaLogInfo {
	var log RustTracingLog
	if err := json.Unmarshal([]byte(body), &log); err != nil {
		return nil
	}

	// Must have at least a level field to be valid
	if log.Level == "" {
		return nil
	}

	info := &SnubaLogInfo{
		Level:     log.Level,
		Target:    log.Target,
		IsJSON:    true,
		RawFields: log.Fields,
	}

	// Extract message from fields
	if log.Fields != nil {
		if msg, ok := log.Fields["message"].(string); ok {
			info.Message = msg
		}
		// Extract storage if present
		if storage, ok := log.Fields["storage"].(string); ok {
			info.Storage = storage
		}
	}

	// If no message in fields, use target as message
	if info.Message == "" && log.Target != "" {
		info.Message = log.Target
	}

	return info
}

// SeverityFromLevel converts a Snuba/Rust log level to OTEL severity.
// Returns severity number and text.
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToUpper(level) {
	case "TRACE":
		return 1, "TRACE"
	case "DEBUG":
		return 5, "DEBUG"
	case "INFO":
		return 9, "INFO"
	case "WARN", "WARNING":
		return 13, "WARN"
	case "ERROR":
		return 17, "ERROR"
	case "FATAL", "CRITICAL":
		return 21, "FATAL"
	default:
		return 9, "INFO"
	}
}

// BuildCleanedMessage creates a human-readable message from parsed fields.
// For Rust logs with target, uses format: [{short_target}] {message}
// For Python logs, just returns the message.
func BuildCleanedMessage(info *SnubaLogInfo) string {
	if !info.IsJSON || info.Target == "" {
		return info.Message
	}

	// Shorten the target path (e.g., "rust_snuba::strategies::clickhouse::writer_v2" -> "writer_v2")
	shortTarget := info.Target
	if idx := strings.LastIndex(info.Target, "::"); idx != -1 {
		shortTarget = info.Target[idx+2:]
	}

	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(shortTarget)
	sb.WriteString("] ")
	sb.WriteString(info.Message)
	return sb.String()
}
