// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klogprocessor

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KlogInfo holds parsed information from a klog entry.
type KlogInfo struct {
	Level     string            // I, W, E, F (Info, Warning, Error, Fatal)
	Timestamp time.Time         // Parsed timestamp
	PID       string            // Process/thread ID
	File      string            // Source file
	Line      string            // Source line number
	Message   string            // The log message
	Extra     map[string]string // Structured key=value pairs
}

// klog format: I1231 12:28:10.685217       1 watcher.go:338] message
// Structured: E1231 12:00:04.713376       1 file.go:174] "Message" key="value"
var (
	// Pattern to detect klog format
	// Level (I/W/E/F) + MMDD + space + HH:MM:SS.microseconds + spaces + PID + space + file:line + ] + message
	klogPattern = regexp.MustCompile(`^([IWEF])(\d{4})\s+(\d{2}:\d{2}:\d{2}\.\d+)\s+(\d+)\s+([^:]+):(\d+)\]\s*(.*)$`)

	// Pattern to extract structured key="value" or key=value pairs
	klogStructuredPattern = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)

	// Pattern to extract quoted message at start: "Message" followed by key=value
	klogQuotedMsgPattern = regexp.MustCompile(`^"([^"]+)"\s*(.*)$`)
)

// IsKlog checks if a log line looks like a klog entry.
func IsKlog(body string) bool {
	if len(body) < 20 {
		return false
	}
	// Quick check: must start with I, W, E, or F followed by 4 digits
	if body[0] != 'I' && body[0] != 'W' && body[0] != 'E' && body[0] != 'F' {
		return false
	}
	if len(body) < 5 {
		return false
	}
	// Check for 4 digits after level
	for i := 1; i < 5; i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return klogPattern.MatchString(body)
}

// ParseKlog parses a klog entry and extracts structured information.
func ParseKlog(body string) *KlogInfo {
	matches := klogPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &KlogInfo{
		Level:   matches[1],
		PID:     matches[4],
		File:    matches[5],
		Line:    matches[6],
		Message: strings.TrimSpace(matches[7]),
		Extra:   make(map[string]string),
	}

	// Parse timestamp: MMDD + HH:MM:SS.microseconds
	// We need to assume current year
	year := time.Now().Year()
	month := matches[2][:2]
	day := matches[2][2:4]
	timeStr := matches[3]

	// Try to parse the timestamp
	tsStr := fmt.Sprintf("%d-%s-%sT%sZ", year, month, day, timeStr)
	if t, err := time.Parse("2006-01-02T15:04:05.000000Z", tsStr); err == nil {
		info.Timestamp = t
	}

	// Check for structured logging format: "Message" key="value"
	if quotedMatch := klogQuotedMsgPattern.FindStringSubmatch(info.Message); quotedMatch != nil {
		info.Message = quotedMatch[1]
		remainder := quotedMatch[2]

		// Extract key=value pairs from remainder
		structuredMatches := klogStructuredPattern.FindAllStringSubmatch(remainder, -1)
		for _, m := range structuredMatches {
			key := m[1]
			value := m[2]
			if value == "" && len(m) > 3 {
				value = m[3]
			}
			info.Extra[key] = value
		}
	}

	return info
}

// SeverityFromKlogLevel converts a klog level to OTEL severity.
func SeverityFromKlogLevel(level string) (int, string) {
	switch level {
	case "I":
		return 9, "INFO"
	case "W":
		return 13, "WARN"
	case "E":
		return 17, "ERROR"
	case "F":
		return 21, "FATAL"
	default:
		return 9, "INFO"
	}
}
