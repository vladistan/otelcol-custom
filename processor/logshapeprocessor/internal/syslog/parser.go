// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslog

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.opentelemetry.io/collector/pdata/plog"
)

type SyslogFormat int

const (
	FormatUnknown SyslogFormat = iota
	FormatRFC3164
	FormatRFC5424
	FormatCiscoIOS
	FormatISO
)

type SyslogInfo struct {
	Format   SyslogFormat
	Time     time.Time // from the wrapper when it carries a zone: ISO and RFC 5424 lines
	Hostname string
	Appname  string // Service/app name (tag in RFC3164, APP-NAME in RFC5424)
	// HostMissing is set for an RFC3164 line with no HOSTNAME: the first token ended in a colon, so it is the tag.
	HostMissing bool
	ProcID      string // Process ID if available
	Content     string // The actual message content

	// Cisco-specific fields
	CiscoFacility string
	CiscoMnemonic string
	CiscoSeq      string
}

var (
	// RFC5424 format: VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
	// Example: "1 2025-12-31T10:05:00.013372-05:00 hostname appname 1234 - - message"
	rfc5424Pattern = regexp.MustCompile(`^(\d+)\s+(\d{4}-\d{2}-\d{2}T[^\s]+)\s+([^\s]+)\s+([^\s]+)\s+([^\s]+)\s+([^\s]+)\s+(\[[^\]]*\]|-)\s*(.*)$`)

	// RFC3164: TIMESTAMP HOSTNAME TAG[PID]: MSG, e.g. "Dec 31 10:05:00 hostname sshd[1234]: message".
	rfc3164Pattern = regexp.MustCompile(`^([A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(\S.*)$`)

	rfc3164TagPattern = regexp.MustCompile(`^([^\s\[:]+)(?:\[(\d+)\])?:\s*(.*)$`)

	// Cisco IOS: SEQ: *TIMESTAMP: %FACILITY-SEVERITY-MNEMONIC: message
	// Example: "123: *Dec 31 10:05:00.123: %SYS-5-CONFIG_I: Configured from console"
	ciscoPattern = regexp.MustCompile(`^(\d+):\s+\*?([A-Za-z]{3}\s+\d{1,2}\s+[\d:.]+):\s+%([^-]+)-(\d)-([^:]+):\s*(.*)$`)

	// ISO wrapper: TIMESTAMP HOSTNAME TAG[PID]: MSG, e.g. "2026-11-20T00:03:18.148343+01:00 nas sshd[12]: text".
	isoPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})) +(\S+)\s+([^\s\[:]+)(?:\[(\d+)\])?:\s*(.*)$`)

	rfc5424PrefixPattern = regexp.MustCompile(`^\d+\s+\d{4}-\d{2}-\d{2}T`)
	rfc3164PrefixPattern = regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}`)
)

func DetectFormat(message string) SyslogFormat {
	message = strings.TrimSpace(message)
	if message == "" {
		return FormatUnknown
	}

	// RFC5424 first: a Cisco sequence number also starts with digits.
	if rfc5424PrefixPattern.MatchString(message) {
		return FormatRFC5424
	}

	if ciscoPattern.MatchString(message) {
		return FormatCiscoIOS
	}

	if rfc3164PrefixPattern.MatchString(message) {
		return FormatRFC3164
	}

	if isoPattern.MatchString(message) {
		return FormatISO
	}

	return FormatUnknown
}

// Parse returns nil for an empty or unrecognised message.
func Parse(message string) *SyslogInfo {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return ParseAs(message, DetectFormat(message))
}

// ParseAs parses the message as the format DetectFormat gave for it, so a caller that detected the
// format does not detect it again.
func ParseAs(message string, format SyslogFormat) *SyslogInfo {
	message = strings.TrimSpace(message)
	switch format {
	case FormatRFC5424:
		return parseRFC5424(message)
	case FormatCiscoIOS:
		return parseCisco(message)
	case FormatRFC3164:
		return parseRFC3164(message)
	case FormatISO:
		return parseISO(message)
	default:
		return nil
	}
}

func parseISO(message string) *SyslogInfo {
	m := isoPattern.FindStringSubmatch(message)
	if m == nil {
		return nil
	}
	info := &SyslogInfo{Format: FormatISO, Hostname: m[2], Appname: m[3], ProcID: m[4], Content: m[5]}
	info.Time, _ = time.Parse(time.RFC3339Nano, m[1])
	return info
}

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
	info.Time, _ = time.Parse(time.RFC3339Nano, matches[2])

	// "-" is the RFC5424 nil value.
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

// The first token after the timestamp is the HOSTNAME. When it ends in a colon and is not an IP address,
// the line has no HOSTNAME and the token is the tag: HostMissing is set and the content is the rest of the
// line as written. An IP address followed by a colon is the host, and the colon is dropped.
func parseRFC3164(message string) *SyslogInfo {
	matches := rfc3164Pattern.FindStringSubmatch(message)
	if matches == nil {
		return nil
	}

	rest := matches[2]
	token, content := rest, ""
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		token = rest[:i]
		content = strings.TrimLeftFunc(rest[i:], unicode.IsSpace)
	}

	info := &SyslogInfo{Format: FormatRFC3164, Hostname: token, Content: content}
	if strings.HasSuffix(token, ":") && !isAddress(token) {
		if addr := strings.TrimSuffix(token, ":"); isAddress(addr) {
			info.Hostname = addr
		} else {
			info.Hostname = ""
			info.HostMissing = true
			content = rest
			info.Content = rest
		}
	}
	if content == "" && !info.HostMissing {
		return nil // a host and nothing else
	}

	if tagMatches := rfc3164TagPattern.FindStringSubmatch(content); tagMatches != nil {
		info.Appname = tagMatches[1]
		info.ProcID = tagMatches[2]
		info.Content = tagMatches[3]
	}

	return info
}

func isAddress(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

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
	}
}

// IsEmbeddedRFC5424 reports double-wrapped BSD syslog: an RFC3164 line whose content is RFC5424.
func IsEmbeddedRFC5424(content string) bool {
	return DetectFormat(content) == FormatRFC5424
}

// MapPriorityToSeverity maps a syslog priority (facility*8 + severity) to an OTEL severity.
// ok is false when the priority is not a number.
func MapPriorityToSeverity(priority string) (number plog.SeverityNumber, text string, ok bool) {
	pri, err := strconv.Atoi(priority)
	if err != nil {
		return plog.SeverityNumberUnspecified, "", false
	}

	switch pri & 0x07 {
	case 0:
		return plog.SeverityNumberFatal4, "FATAL", true
	case 1:
		return plog.SeverityNumberFatal, "FATAL", true
	case 2, 3:
		return plog.SeverityNumberError, "ERROR", true
	case 4:
		return plog.SeverityNumberWarn, "WARN", true
	case 5, 6:
		return plog.SeverityNumberInfo, "INFO", true
	default:
		return plog.SeverityNumberDebug, "DEBUG", true
	}
}
