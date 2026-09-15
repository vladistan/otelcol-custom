// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

import (
	"regexp"
	"strconv"
	"strings"
)

// UvicornLogInfo holds parsed information from a Uvicorn access log entry.
type UvicornLogInfo struct {
	Level      string // INFO, WARNING, ERROR, DEBUG, CRITICAL
	ClientAddr string // IP:port or IP
	ClientIP   string // Just the IP
	ClientPort string // Just the port (if present)
	Method     string // HTTP method
	Path       string // URL path
	Query      string // Query string (if present)
	Protocol   string // HTTP/1.1, HTTP/2, etc.
	StatusCode int    // HTTP status code
	StatusText string // OK, Not Found, etc.
}

// Uvicorn access log format: LEVEL:     CLIENT_ADDR - "METHOD PATH PROTOCOL" STATUS_CODE STATUS_TEXT
// Examples:
//   - INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK
//   - WARNING:  192.168.1.1:8080 - "POST /api/submit HTTP/1.1" 400 Bad Request
//   - ERROR:    10.0.0.1 - "GET /api/error HTTP/1.1" 500 Internal Server Error
var uvicornLogPattern = regexp.MustCompile(
	`^(INFO|WARNING|ERROR|DEBUG|CRITICAL):\s+` + // level:
		`([^\s]+)\s+-\s+` + // client address
		`"([A-Z]+)\s+` + // method
		`([^\s?]+)` + // path
		`(?:\?([^\s]*))?\s+` + // query (optional)
		`(HTTP/[^\s"]+)"\s+` + // protocol
		`(\d+)` + // status code
		`(?:\s+(.*))?$`, // status text (optional)
)

// IsUvicornLog checks if a log line looks like a Uvicorn access log entry.
func IsUvicornLog(body string) bool {
	if len(body) < 20 {
		return false
	}

	// Quick check: must start with level followed by colon and spaces
	return strings.HasPrefix(body, "INFO:") ||
		strings.HasPrefix(body, "WARNING:") ||
		strings.HasPrefix(body, "ERROR:") ||
		strings.HasPrefix(body, "DEBUG:") ||
		strings.HasPrefix(body, "CRITICAL:")
}

// ParseUvicornLog parses a Uvicorn access log entry and extracts structured information.
func ParseUvicornLog(body string) *UvicornLogInfo {
	matches := uvicornLogPattern.FindStringSubmatch(body)
	if len(matches) < 8 {
		return nil
	}

	statusCode, _ := strconv.Atoi(matches[7])

	info := &UvicornLogInfo{
		Level:      matches[1],
		ClientAddr: matches[2],
		Method:     matches[3],
		Path:       matches[4],
		Query:      matches[5],
		Protocol:   matches[6],
		StatusCode: statusCode,
		StatusText: strings.TrimSpace(matches[8]),
	}

	// Parse client address into IP and port
	if colonIdx := strings.LastIndex(info.ClientAddr, ":"); colonIdx > 0 {
		// Check if it's an IPv6 address
		if strings.Count(info.ClientAddr, ":") > 1 {
			// IPv6 - handle [ip]:port format
			if strings.HasPrefix(info.ClientAddr, "[") {
				if bracketIdx := strings.Index(info.ClientAddr, "]:"); bracketIdx > 0 {
					info.ClientIP = info.ClientAddr[1:bracketIdx]
					info.ClientPort = info.ClientAddr[bracketIdx+2:]
				} else {
					info.ClientIP = info.ClientAddr
				}
			} else {
				info.ClientIP = info.ClientAddr
			}
		} else {
			// IPv4:port
			info.ClientIP = info.ClientAddr[:colonIdx]
			info.ClientPort = info.ClientAddr[colonIdx+1:]
		}
	} else {
		info.ClientIP = info.ClientAddr
	}

	return info
}

// SeverityFromLevel converts a Uvicorn log level to OTEL severity.
// Returns severity number and text.
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 5, "DEBUG"
	case "INFO":
		return 9, "INFO"
	case "WARNING":
		return 13, "WARN"
	case "ERROR":
		return 17, "ERROR"
	case "CRITICAL":
		return 21, "FATAL"
	default:
		return 9, "INFO"
	}
}

// SeverityFromStatusCode returns OTEL severity based on HTTP status code.
func SeverityFromStatusCode(statusCode int) (int, string) {
	if statusCode >= 500 {
		return 17, "ERROR"
	} else if statusCode >= 400 {
		return 13, "WARN"
	}
	return 9, "INFO"
}

// BuildCleanedMessage creates a human-readable message from parsed fields.
// Format: {status_code} {method} {path}
func BuildCleanedMessage(info *UvicornLogInfo) string {
	var sb strings.Builder
	sb.WriteString(strconv.Itoa(info.StatusCode))
	sb.WriteString(" ")
	sb.WriteString(info.Method)
	sb.WriteString(" ")
	sb.WriteString(info.Path)
	if info.Query != "" {
		sb.WriteString("?")
		sb.WriteString(info.Query)
	}
	return sb.String()
}
