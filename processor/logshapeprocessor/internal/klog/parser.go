// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klog

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KV is one structured key and value in the order they were written.
type KV struct{ Key, Value string }

// KlogInfo holds parsed information from a klog entry.
type KlogInfo struct {
	Level     string            // I, W, E, F (Info, Warning, Error, Fatal)
	MonthDay  string            // Month and day as written; the line carries no year
	Clock     string            // HH:MM:SS.ffffff as written
	Pairs     []KV              // Structured key=value pairs in line order
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
	// Level letter + month and day + space + HH:MM:SS.microseconds + spaces + PID + space + file:line + ] + message
	klogPattern = regexp.MustCompile(`^(I|W|E|F)(\d{4})\s+(\d{2}:\d{2}:\d{2}\.\d+)\s+(\d+)\s+([^:]+):(\d+)\]\s*(.*)$`)

	// Pattern to extract structured key="value" or key=value pairs
	klogStructuredPattern = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)

	// Pattern to extract quoted message at start: "Message" followed by key=value
	klogQuotedMsgPattern = regexp.MustCompile(`^"([^"]+)"\s*(.*)$`)
)

// QuickCheck is the part of IsKlog that needs no regular expression: a level letter and four digits.
func QuickCheck(body string) bool {
	if len(body) < 20 {
		return false
	}
	if body[0] != 'I' && body[0] != 'W' && body[0] != 'E' && body[0] != 'F' {
		return false
	}
	for i := 1; i < 5; i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return true
}

// IsKlog checks if a log line looks like a klog entry.
func IsKlog(body string) bool {
	return QuickCheck(body) && klogPattern.MatchString(body)
}

// ParseKlog parses a klog entry and extracts structured information.
func ParseKlog(body string) *KlogInfo {
	matches := klogPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &KlogInfo{
		Level:    matches[1],
		MonthDay: matches[2],
		Clock:    matches[3],
		PID:      matches[4],
		File:     matches[5],
		Line:     matches[6],
		Message:  strings.TrimSpace(matches[7]),
		Extra:    make(map[string]string),
	}

	// Parse timestamp: month and day + HH:MM:SS.microseconds
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
			info.Pairs = append(info.Pairs, KV{key, value})
		}
	}

	return info
}
