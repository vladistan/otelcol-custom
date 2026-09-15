// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logruslogprocessor

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LogrusLogInfo holds parsed information from a Logrus/Zap JSON log entry.
type LogrusLogInfo struct {
	Level      string            // debug, info, warning, error, fatal, panic
	Message    string            // The log message
	Timestamp  time.Time         // Parsed timestamp
	Caller     string            // Source file:line (zap style)
	Controller string            // Controller name (K8s controller-runtime)
	Error      string            // Error message
	Stacktrace string            // Stack trace (for errors)
	Extra      map[string]string // Additional fields from the JSON
}

// IsLogrusJSON checks if a log line looks like a Logrus/Zap JSON log entry.
// Logrus format: {"level":"info","msg":"message","time":"2025-12-31T04:03:58Z"}
// Zap format: {"level":"info","ts":"2025-12-31T04:03:58Z","caller":"file.go:123",...}
func IsLogrusJSON(body string) bool {
	if len(body) < 10 {
		return false
	}

	// Must start with { and be valid-ish JSON
	if body[0] != '{' {
		return false
	}

	// Must contain "level" or "severity" field
	hasLevel := strings.Contains(body, `"level"`) || strings.Contains(body, `"Level"`) ||
		strings.Contains(body, `"severity"`) || strings.Contains(body, `"Severity"`)
	if !hasLevel {
		return false
	}

	// Either has msg/message OR has ts/time (zap style without explicit msg)
	hasMsg := strings.Contains(body, `"msg"`) || strings.Contains(body, `"message"`) ||
		strings.Contains(body, `"Msg"`) || strings.Contains(body, `"Message"`)
	hasTimestamp := strings.Contains(body, `"ts"`) || strings.Contains(body, `"time"`) ||
		strings.Contains(body, `"Time"`) || strings.Contains(body, `"@timestamp"`)

	return hasMsg || hasTimestamp
}

// ParseLogrusJSON parses a Logrus JSON log entry and extracts structured information.
func ParseLogrusJSON(body string) *LogrusLogInfo {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}

	info := &LogrusLogInfo{
		Extra: make(map[string]string),
	}

	// Extract level (try common field names, including "severity" for knative/zap)
	if v, ok := getStringField(raw, "level", "Level", "severity", "Severity"); ok {
		info.Level = strings.ToLower(v)
	}

	// Extract message (try common field names)
	if v, ok := getStringField(raw, "msg", "message", "Msg", "Message"); ok {
		info.Message = v
	}

	// Extract timestamp (try common field names)
	if v, ok := getStringField(raw, "time", "timestamp", "Time", "Timestamp", "ts", "@timestamp"); ok {
		// Try parsing various time formats
		info.Timestamp = parseTimestamp(v)
	}

	// Extract zap-style fields
	if v, ok := getStringField(raw, "caller"); ok {
		info.Caller = v
	}
	if v, ok := getStringField(raw, "controller"); ok {
		info.Controller = v
	}
	if v, ok := getStringField(raw, "error"); ok {
		info.Error = v
	}
	if v, ok := getStringField(raw, "stacktrace"); ok {
		info.Stacktrace = v
	}

	// If no message found, try to construct one from known fields (zap style)
	// or leave empty (processor will keep original body)
	if info.Message == "" {
		// Try common zap-style action fields
		for _, key := range []string{"start reconcile", "end reconcile", "event"} {
			if v, ok := raw[key].(string); ok {
				info.Message = key + ": " + v
				break
			}
		}
	}

	// Extract extra fields (exclude known fields)
	knownFields := map[string]bool{
		"level": true, "Level": true, "severity": true, "Severity": true,
		"msg": true, "message": true, "Msg": true, "Message": true,
		"time": true, "timestamp": true, "Time": true, "Timestamp": true, "ts": true, "@timestamp": true,
		"caller": true, "controller": true, "error": true, "stacktrace": true,
		"start reconcile": true, "end reconcile": true, "event": true,
	}

	for k, v := range raw {
		if !knownFields[k] {
			if str, ok := v.(string); ok {
				info.Extra[k] = str
			}
		}
	}

	return info
}

// getStringField tries to get a string value from the map using multiple possible keys.
func getStringField(raw map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			if str, ok := v.(string); ok {
				return str, true
			}
		}
	}
	return "", false
}

// parseTimestamp attempts to parse a timestamp string in various formats.
func parseTimestamp(s string) time.Time {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.000",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}

	return time.Time{}
}

// SeverityFromLevel converts a Logrus log level to OTEL severity.
// Returns severity number and text.
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToLower(level) {
	case "trace":
		return 1, "TRACE"
	case "debug":
		return 5, "DEBUG"
	case "info":
		return 9, "INFO"
	case "warn", "warning":
		return 13, "WARN"
	case "error":
		return 17, "ERROR"
	case "fatal":
		return 21, "FATAL"
	case "panic":
		return 21, "FATAL"
	default:
		return 9, "INFO"
	}
}

// Logrus text format patterns
// Format: time="2025-12-31T04:25:29Z" level=info msg="All records are already up to date"
var (
	// Pattern to detect logrus text format - must have level= and msg=
	logrusTextPattern = regexp.MustCompile(`^time="[^"]*"\s+level=\w+\s+msg="`)
	// Pattern to extract key=value or key="value" pairs
	logrusTextFieldPattern = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)
)

// Zap console format patterns
// Format: 2026-01-02T14:18:43.362Z	info	internal/retry_sender.go:133	Exporting failed...	{JSON}
var (
	// Pattern to detect zap console format - timestamp, level, caller, message (tab-separated)
	// Level must be one of: debug, info, warn, error, dpanic, panic, fatal
	zapConsolePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z?\t(?:debug|info|warn|error|dpanic|panic|fatal)\t`)
)

// IsLogrusText checks if a log line looks like a Logrus text format entry.
// Format: time="2025-12-31T04:25:29Z" level=info msg="message" [key="value"...]
func IsLogrusText(body string) bool {
	if len(body) < 20 {
		return false
	}
	return logrusTextPattern.MatchString(body)
}

// ParseLogrusText parses a Logrus text format log entry and extracts structured information.
func ParseLogrusText(body string) *LogrusLogInfo {
	if !IsLogrusText(body) {
		return nil
	}

	info := &LogrusLogInfo{
		Extra: make(map[string]string),
	}

	// Extract all key=value pairs
	matches := logrusTextFieldPattern.FindAllStringSubmatch(body, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		key := match[1]
		// Value is either in group 2 (quoted) or group 3 (unquoted)
		value := match[2]
		if value == "" && len(match) > 3 {
			value = match[3]
		}

		switch key {
		case "time":
			info.Timestamp = parseTimestamp(value)
		case "level":
			info.Level = strings.ToLower(value)
		case "msg":
			info.Message = value
		case "caller":
			info.Caller = value
		case "error":
			info.Error = value
		default:
			info.Extra[key] = value
		}
	}

	return info
}

// IsZapConsole checks if a log line looks like a Zap console format entry.
// Format: 2026-01-02T14:18:43.362Z	info	file.go:123	message	{optional JSON}
func IsZapConsole(body string) bool {
	if len(body) < 30 {
		return false
	}
	return zapConsolePattern.MatchString(body)
}

// ParseZapConsole parses a Zap console format log entry and extracts structured information.
// Format: timestamp\tlevel\tcaller\tmessage\t{optional JSON context}
func ParseZapConsole(body string) *LogrusLogInfo {
	if !IsZapConsole(body) {
		return nil
	}

	// Split by tabs
	parts := strings.Split(body, "\t")
	if len(parts) < 4 {
		return nil
	}

	info := &LogrusLogInfo{
		Extra: make(map[string]string),
	}

	// Part 0: timestamp
	info.Timestamp = parseTimestamp(parts[0])

	// Part 1: level
	info.Level = strings.ToLower(parts[1])

	// Part 2: caller (file.go:123)
	info.Caller = parts[2]

	// Part 3: message
	info.Message = parts[3]

	// Part 4+: optional JSON context (may contain tabs)
	if len(parts) > 4 {
		jsonPart := strings.Join(parts[4:], "\t")
		jsonPart = strings.TrimSpace(jsonPart)

		// Parse the JSON context if present
		if strings.HasPrefix(jsonPart, "{") {
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(jsonPart), &raw); err == nil {
				// Extract known fields
				if v, ok := getStringField(raw, "error"); ok {
					info.Error = v
				}
				if v, ok := getStringField(raw, "stacktrace"); ok {
					info.Stacktrace = v
				}

				// Known fields to skip (not useful as attributes)
				skipFields := map[string]bool{
					"error": true, "stacktrace": true,
					"resource": true, // nested object from otel collector
				}

				// Extract extra fields as strings
				for k, v := range raw {
					if skipFields[k] {
						continue
					}
					switch val := v.(type) {
					case string:
						info.Extra[k] = val
					case float64:
						info.Extra[k] = formatFloat(val)
					case bool:
						if val {
							info.Extra[k] = "true"
						} else {
							info.Extra[k] = "false"
						}
					}
				}
			}
		}
	}

	return info
}

// formatFloat formats a float64 as a string.
func formatFloat(f float64) string {
	// Check if it's an integer
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
