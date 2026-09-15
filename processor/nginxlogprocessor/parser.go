// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NginxLogInfo contains parsed Nginx access log fields.
type NginxLogInfo struct {
	ClientAddr   string
	RemoteUser   string
	Timestamp    time.Time
	TimestampRaw string
	Method       string
	Path         string
	Query        string
	Protocol     string
	StatusCode   int
	BodyBytes    int64
	Referer      string
	UserAgent    string
	ForwardedFor string
	CleanMessage string
}

// Nginx combined log format regex
// Format: $remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" "$http_x_forwarded_for"
// Example: 192.0.2.1 - - [31/Dec/2025:00:30:00 +0000] "POST /api/2/envelope/ HTTP/1.1" 200 41 "-" "sentry.go/0.40.0" "192.0.2.1:46331"
var nginxCombinedRegex = regexp.MustCompile(
	`^(\S+)\s+-\s+(\S+)\s+\[([^\]]+)\]\s+"([^"]+)"\s+(\d+)\s+(\d+)\s+"([^"]*)"\s+"([^"]*)"(?:\s+"([^"]*)")?`,
)

// Request line regex: METHOD PATH PROTOCOL
var requestLineRegex = regexp.MustCompile(`^(\S+)\s+(\S+?)(?:\?(\S+))?\s+(\S+)$`)

// ParseNginxLog attempts to parse a Nginx combined log format line.
// Returns nil if the line doesn't match the expected format.
func ParseNginxLog(line string) *NginxLogInfo {
	// Handle JSON wrapper if present (journald wraps in {"text": "..."})
	if strings.HasPrefix(line, `{"text":`) {
		// Extract the text field value
		start := strings.Index(line, `"text":"`)
		if start != -1 {
			start += 8 // len(`"text":"`)
			end := strings.LastIndex(line, `"}`)
			if end > start {
				line = line[start:end]
				// Unescape JSON string
				line = strings.ReplaceAll(line, `\"`, `"`)
				line = strings.ReplaceAll(line, `\\`, `\`)
			}
		}
	}

	matches := nginxCombinedRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	info := &NginxLogInfo{
		ClientAddr:   matches[1],
		RemoteUser:   matches[2],
		TimestampRaw: matches[3],
		Referer:      matches[7],
		UserAgent:    matches[8],
	}

	// Parse X-Forwarded-For if present
	if len(matches) > 9 && matches[9] != "" && matches[9] != "-" {
		info.ForwardedFor = matches[9]
	}

	// Parse status code
	if status, err := strconv.Atoi(matches[5]); err == nil {
		info.StatusCode = status
	}

	// Parse body bytes
	if bytes, err := strconv.ParseInt(matches[6], 10, 64); err == nil {
		info.BodyBytes = bytes
	}

	// Parse request line: "METHOD PATH PROTOCOL"
	requestLine := matches[4]
	if reqMatches := requestLineRegex.FindStringSubmatch(requestLine); reqMatches != nil {
		info.Method = reqMatches[1]
		info.Path = reqMatches[2]
		if len(reqMatches) > 3 {
			info.Query = reqMatches[3]
		}
		if len(reqMatches) > 4 {
			info.Protocol = reqMatches[4]
		}
	} else {
		// Fallback: just store the whole request line as path
		info.Path = requestLine
	}

	// Parse timestamp
	// Format: 31/Dec/2025:00:30:00 +0000
	if ts, err := time.Parse("02/Jan/2006:15:04:05 -0700", info.TimestampRaw); err == nil {
		info.Timestamp = ts
	}

	// Clean remote user (replace "-" with empty)
	if info.RemoteUser == "-" {
		info.RemoteUser = ""
	}

	// Clean referer (replace "-" with empty)
	if info.Referer == "-" {
		info.Referer = ""
	}

	// Build clean message: METHOD PATH STATUS
	info.CleanMessage = info.Method + " " + info.Path
	if info.Query != "" {
		info.CleanMessage += "?" + info.Query
	}
	info.CleanMessage += " " + strconv.Itoa(info.StatusCode)

	return info
}

// IsNginxLog returns true if the line appears to be a Nginx log.
// This is a quick check before attempting full parsing.
func IsNginxLog(line string) bool {
	// Quick heuristic: contains typical Nginx log markers
	// Look for: IP - user [timestamp] "METHOD
	return strings.Contains(line, "] \"") &&
		(strings.Contains(line, " - - [") || strings.Contains(line, " - ") && strings.Contains(line, "["))
}
