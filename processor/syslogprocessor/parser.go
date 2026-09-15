// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"regexp"
	"strconv"
	"strings"
)

// SyslogFormat represents the detected syslog message format.
type SyslogFormat int

const (
	FormatUnknown SyslogFormat = iota
	FormatRFC3164
	FormatRFC5424
	FormatCiscoIOS
)

// SyslogInfo holds parsed syslog information.
type SyslogInfo struct {
	Format   SyslogFormat
	Hostname string
	Appname  string // Service/app name (tag in RFC3164, APP-NAME in RFC5424)
	ProcID   string // Process ID if available
	Content  string // The actual message content

	// Cisco-specific fields
	CiscoFacility string
	CiscoMnemonic string
	CiscoSeq      string
}

// Regular expressions for format detection and parsing
var (
	// RFC5424 format: VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
	// Example: "1 2025-12-31T10:05:00.013372-05:00 hostname appname 1234 - - message"
	rfc5424Pattern = regexp.MustCompile(`^(\d+)\s+(\d{4}-\d{2}-\d{2}T[^\s]+)\s+([^\s]+)\s+([^\s]+)\s+([^\s]+)\s+([^\s]+)\s+(\[[^\]]*\]|-)\s*(.*)$`)

	// RFC3164 format: TIMESTAMP HOSTNAME TAG[PID]: MSG
	// Example: "Dec 31 10:05:00 hostname sshd[1234]: message"
	// Note: TAG may include path like /usr/sbin/cron, and PID is optional
	rfc3164Pattern = regexp.MustCompile(`^([A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+([^\s]+)\s+(.*)$`)

	// RFC3164 tag extraction: tag[pid]: message or tag: message
	rfc3164TagPattern = regexp.MustCompile(`^([^\s\[:]+)(?:\[(\d+)\])?:\s*(.*)$`)

	// Cisco IOS format: SEQ: *TIMESTAMP: %FACILITY-SEVERITY-MNEMONIC: message
	// Example: "123: *Dec 31 10:05:00.123: %SYS-5-CONFIG_I: Configured from console"
	ciscoPattern = regexp.MustCompile(`^(\d+):\s+\*?([A-Za-z]{3}\s+\d{1,2}\s+[\d:.]+):\s+%([^-]+)-(\d)-([^:]+):\s*(.*)$`)
)

// DetectFormat detects the syslog message format.
func DetectFormat(message string) SyslogFormat {
	message = strings.TrimSpace(message)
	if message == "" {
		return FormatUnknown
	}

	// Check RFC5424 first (starts with version digit followed by ISO timestamp)
	if len(message) > 0 && message[0] >= '0' && message[0] <= '9' {
		if regexp.MustCompile(`^\d+\s+\d{4}-\d{2}-\d{2}T`).MatchString(message) {
			return FormatRFC5424
		}
	}

	// Check Cisco IOS format (starts with sequence number)
	if ciscoPattern.MatchString(message) {
		return FormatCiscoIOS
	}

	// Check RFC3164 (starts with month abbreviation timestamp)
	if regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}`).MatchString(message) {
		return FormatRFC3164
	}

	return FormatUnknown
}

// Parse parses a syslog message and returns structured information.
func Parse(message string) *SyslogInfo {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}

	format := DetectFormat(message)
	switch format {
	case FormatRFC5424:
		return parseRFC5424(message)
	case FormatCiscoIOS:
		return parseCisco(message)
	case FormatRFC3164:
		return parseRFC3164(message)
	default:
		return nil
	}
}

// parseRFC5424 parses an RFC5424 formatted syslog message.
func parseRFC5424(message string) *SyslogInfo {
	matches := rfc5424Pattern.FindStringSubmatch(message)
	if matches == nil {
		return nil
	}

	info := &SyslogInfo{
		Format:   FormatRFC5424,
		Hostname: matches[3],
		Appname:  matches[4],
		ProcID:   matches[5],
		Content:  matches[8],
	}

	// Clean up nil-value indicators
	if info.Hostname == "-" {
		info.Hostname = ""
	}
	if info.Appname == "-" {
		info.Appname = ""
	}
	if info.ProcID == "-" {
		info.ProcID = ""
	}

	return info
}

// parseRFC3164 parses an RFC3164 formatted syslog message.
func parseRFC3164(message string) *SyslogInfo {
	matches := rfc3164Pattern.FindStringSubmatch(message)
	if matches == nil {
		return nil
	}

	info := &SyslogInfo{
		Format:   FormatRFC3164,
		Hostname: matches[2],
		Content:  matches[3], // Will be refined below
	}

	// Try to extract tag and PID from content
	content := matches[3]
	if tagMatches := rfc3164TagPattern.FindStringSubmatch(content); tagMatches != nil {
		info.Appname = tagMatches[1]
		info.ProcID = tagMatches[2]
		info.Content = tagMatches[3]
	}

	return info
}

// parseCisco parses a Cisco IOS formatted syslog message.
func parseCisco(message string) *SyslogInfo {
	matches := ciscoPattern.FindStringSubmatch(message)
	if matches == nil {
		return nil
	}

	return &SyslogInfo{
		Format:        FormatCiscoIOS,
		CiscoSeq:      matches[1],
		CiscoFacility: matches[3],
		CiscoMnemonic: matches[5],
		Content:       matches[6],
		// Note: Cisco syslog doesn't have hostname in the message itself
		// It should come from the source IP or be configured separately
	}
}

// IsEmbeddedRFC5424 checks if an RFC3164 message content is actually RFC5424.
// This handles double-wrapped BSD syslog where the outer RFC3164 wraps inner RFC5424.
func IsEmbeddedRFC5424(content string) bool {
	return DetectFormat(content) == FormatRFC5424
}

// MapPriorityToSeverity converts syslog priority to OTEL severity number and text.
// Syslog priority = (facility * 8) + severity
// Severity: 0=Emergency, 1=Alert, 2=Critical, 3=Error, 4=Warning, 5=Notice, 6=Info, 7=Debug
func MapPriorityToSeverity(priority string) (severityNumber int32, severityText string) {
	pri, err := strconv.Atoi(priority)
	if err != nil {
		return 0, ""
	}

	// Extract severity from priority (lower 3 bits)
	severity := pri & 0x07

	switch severity {
	case 0: // Emergency
		return 24, "FATAL" // SEVERITY_NUMBER_FATAL4
	case 1: // Alert
		return 21, "FATAL" // SEVERITY_NUMBER_FATAL
	case 2: // Critical
		return 17, "ERROR" // SEVERITY_NUMBER_ERROR
	case 3: // Error
		return 17, "ERROR" // SEVERITY_NUMBER_ERROR
	case 4: // Warning
		return 13, "WARN" // SEVERITY_NUMBER_WARN
	case 5: // Notice
		return 9, "INFO" // SEVERITY_NUMBER_INFO
	case 6: // Informational
		return 9, "INFO" // SEVERITY_NUMBER_INFO
	case 7: // Debug
		return 5, "DEBUG" // SEVERITY_NUMBER_DEBUG
	default:
		return 0, ""
	}
}
