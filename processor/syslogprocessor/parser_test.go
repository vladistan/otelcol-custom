// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected SyslogFormat
	}{
		{
			name:     "RFC5424 standard",
			message:  "1 2025-12-31T10:05:00.013372-05:00 hostname appname 1234 - - This is the message",
			expected: FormatRFC5424,
		},
		{
			name:     "RFC5424 with structured data",
			message:  "1 2025-12-31T10:05:00-05:00 host app 1234 msgid [exampleSDID@32473 iut=\"3\"] message",
			expected: FormatRFC5424,
		},
		{
			name:     "RFC3164 standard",
			message:  "Dec 31 10:05:00 hostname sshd[1234]: Failed password for invalid user admin",
			expected: FormatRFC3164,
		},
		{
			name:     "RFC3164 without PID",
			message:  "Dec 31 10:05:00 hostname sshd: Connection closed",
			expected: FormatRFC3164,
		},
		{
			name:     "RFC3164 single digit day",
			message:  "Jan  1 00:00:00 hostname kernel: message",
			expected: FormatRFC3164,
		},
		{
			name:     "Cisco IOS format",
			message:  "123: *Dec 31 10:05:00.123: %SYS-5-CONFIG_I: Configured from console",
			expected: FormatCiscoIOS,
		},
		{
			name:     "Cisco IOS without asterisk",
			message:  "456: Dec 31 10:05:00.123: %LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
			expected: FormatCiscoIOS,
		},
		{
			name:     "Empty message",
			message:  "",
			expected: FormatUnknown,
		},
		{
			name:     "Unknown format",
			message:  "Just some random text that doesn't match any format",
			expected: FormatUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectFormat(tt.message)
			if result != tt.expected {
				t.Errorf("DetectFormat(%q) = %v, want %v", tt.message, result, tt.expected)
			}
		})
	}
}

func TestParseRFC5424(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected *SyslogInfo
	}{
		{
			name:    "Standard RFC5424",
			message: "1 2025-12-31T10:05:00.013372-05:00 storage1.home.example.com smartd 1501 - - Device: /dev/ada0, SMART Usage",
			expected: &SyslogInfo{
				Format:   FormatRFC5424,
				Hostname: "storage1.home.example.com",
				Appname:  "smartd",
				ProcID:   "1501",
				Content:  "Device: /dev/ada0, SMART Usage",
			},
		},
		{
			name:    "RFC5424 with cron",
			message: "1 2025-12-31T10:05:00.013372-05:00 storage1.homelab.home.example.com /usr/sbin/cron 19467 - - (root) CMD (/usr/libexec/atrun)",
			expected: &SyslogInfo{
				Format:   FormatRFC5424,
				Hostname: "storage1.homelab.home.example.com",
				Appname:  "/usr/sbin/cron",
				ProcID:   "19467",
				Content:  "(root) CMD (/usr/libexec/atrun)",
			},
		},
		{
			name:    "RFC5424 with nil values",
			message: "1 2025-12-31T10:05:00-05:00 - - - - - Just a message",
			expected: &SyslogInfo{
				Format:   FormatRFC5424,
				Hostname: "",
				Appname:  "",
				ProcID:   "",
				Content:  "Just a message",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.message)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Parse(%q) = %+v, want nil", tt.message, result)
				}
				return
			}

			if result == nil {
				t.Errorf("Parse(%q) = nil, want %+v", tt.message, tt.expected)
				return
			}

			if result.Format != tt.expected.Format {
				t.Errorf("Format = %v, want %v", result.Format, tt.expected.Format)
			}
			if result.Hostname != tt.expected.Hostname {
				t.Errorf("Hostname = %q, want %q", result.Hostname, tt.expected.Hostname)
			}
			if result.Appname != tt.expected.Appname {
				t.Errorf("Appname = %q, want %q", result.Appname, tt.expected.Appname)
			}
			if result.ProcID != tt.expected.ProcID {
				t.Errorf("ProcID = %q, want %q", result.ProcID, tt.expected.ProcID)
			}
			if result.Content != tt.expected.Content {
				t.Errorf("Content = %q, want %q", result.Content, tt.expected.Content)
			}
		})
	}
}

func TestParseRFC3164(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected *SyslogInfo
	}{
		{
			name:    "Standard with PID",
			message: "Dec 31 10:05:00 hostname sshd[1234]: Failed password for invalid user admin",
			expected: &SyslogInfo{
				Format:   FormatRFC3164,
				Hostname: "hostname",
				Appname:  "sshd",
				ProcID:   "1234",
				Content:  "Failed password for invalid user admin",
			},
		},
		{
			name:    "Without PID",
			message: "Dec 31 10:05:00 hostname kernel: USB device connected",
			expected: &SyslogInfo{
				Format:   FormatRFC3164,
				Hostname: "hostname",
				Appname:  "kernel",
				ProcID:   "",
				Content:  "USB device connected",
			},
		},
		{
			name:    "Single digit day",
			message: "Jan  1 00:00:00 hostname crond[1234]: Job started",
			expected: &SyslogInfo{
				Format:   FormatRFC3164,
				Hostname: "hostname",
				Appname:  "crond",
				ProcID:   "1234",
				Content:  "Job started",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.message)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Parse(%q) = %+v, want nil", tt.message, result)
				}
				return
			}

			if result == nil {
				t.Errorf("Parse(%q) = nil, want %+v", tt.message, tt.expected)
				return
			}

			if result.Format != tt.expected.Format {
				t.Errorf("Format = %v, want %v", result.Format, tt.expected.Format)
			}
			if result.Hostname != tt.expected.Hostname {
				t.Errorf("Hostname = %q, want %q", result.Hostname, tt.expected.Hostname)
			}
			if result.Appname != tt.expected.Appname {
				t.Errorf("Appname = %q, want %q", result.Appname, tt.expected.Appname)
			}
			if result.ProcID != tt.expected.ProcID {
				t.Errorf("ProcID = %q, want %q", result.ProcID, tt.expected.ProcID)
			}
			if result.Content != tt.expected.Content {
				t.Errorf("Content = %q, want %q", result.Content, tt.expected.Content)
			}
		})
	}
}

func TestParseCisco(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected *SyslogInfo
	}{
		{
			name:    "Standard Cisco with asterisk",
			message: "123: *Dec 31 10:05:00.123: %SYS-5-CONFIG_I: Configured from console by admin",
			expected: &SyslogInfo{
				Format:        FormatCiscoIOS,
				CiscoSeq:      "123",
				CiscoFacility: "SYS",
				CiscoMnemonic: "CONFIG_I",
				Content:       "Configured from console by admin",
			},
		},
		{
			name:    "Cisco without asterisk",
			message: "456: Dec 31 10:05:00.123: %LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
			expected: &SyslogInfo{
				Format:        FormatCiscoIOS,
				CiscoSeq:      "456",
				CiscoFacility: "LINK",
				CiscoMnemonic: "UPDOWN",
				Content:       "Interface GigabitEthernet0/1, changed state to up",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.message)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Parse(%q) = %+v, want nil", tt.message, result)
				}
				return
			}

			if result == nil {
				t.Errorf("Parse(%q) = nil, want %+v", tt.message, tt.expected)
				return
			}

			if result.Format != tt.expected.Format {
				t.Errorf("Format = %v, want %v", result.Format, tt.expected.Format)
			}
			if result.CiscoSeq != tt.expected.CiscoSeq {
				t.Errorf("CiscoSeq = %q, want %q", result.CiscoSeq, tt.expected.CiscoSeq)
			}
			if result.CiscoFacility != tt.expected.CiscoFacility {
				t.Errorf("CiscoFacility = %q, want %q", result.CiscoFacility, tt.expected.CiscoFacility)
			}
			if result.CiscoMnemonic != tt.expected.CiscoMnemonic {
				t.Errorf("CiscoMnemonic = %q, want %q", result.CiscoMnemonic, tt.expected.CiscoMnemonic)
			}
			if result.Content != tt.expected.Content {
				t.Errorf("Content = %q, want %q", result.Content, tt.expected.Content)
			}
		})
	}
}

func TestIsEmbeddedRFC5424(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "Embedded RFC5424",
			content:  "1 2025-12-31T10:05:00-05:00 host smartd 1501 - - Device: /dev/ada0",
			expected: true,
		},
		{
			name:     "Regular RFC3164 content",
			content:  "Device: /dev/ada0, SMART Usage",
			expected: false,
		},
		{
			name:     "Empty content",
			content:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsEmbeddedRFC5424(tt.content)
			if result != tt.expected {
				t.Errorf("IsEmbeddedRFC5424(%q) = %v, want %v", tt.content, result, tt.expected)
			}
		})
	}
}

func TestMapPriorityToSeverity(t *testing.T) {
	tests := []struct {
		name           string
		priority       string
		expectedNumber int32
		expectedText   string
	}{
		{
			name:           "Emergency (priority 0 = facility 0 + severity 0)",
			priority:       "0",
			expectedNumber: 24, // SEVERITY_NUMBER_FATAL4
			expectedText:   "FATAL",
		},
		{
			name:           "Alert (priority 1)",
			priority:       "1",
			expectedNumber: 21, // SEVERITY_NUMBER_FATAL
			expectedText:   "FATAL",
		},
		{
			name:           "Critical (priority 2)",
			priority:       "2",
			expectedNumber: 17, // SEVERITY_NUMBER_ERROR
			expectedText:   "ERROR",
		},
		{
			name:           "Error (priority 3)",
			priority:       "3",
			expectedNumber: 17, // SEVERITY_NUMBER_ERROR
			expectedText:   "ERROR",
		},
		{
			name:           "Warning (priority 4)",
			priority:       "4",
			expectedNumber: 13, // SEVERITY_NUMBER_WARN
			expectedText:   "WARN",
		},
		{
			name:           "Notice (priority 5)",
			priority:       "5",
			expectedNumber: 9, // SEVERITY_NUMBER_INFO
			expectedText:   "INFO",
		},
		{
			name:           "Info (priority 6)",
			priority:       "6",
			expectedNumber: 9, // SEVERITY_NUMBER_INFO
			expectedText:   "INFO",
		},
		{
			name:           "Debug (priority 7)",
			priority:       "7",
			expectedNumber: 5, // SEVERITY_NUMBER_DEBUG
			expectedText:   "DEBUG",
		},
		{
			name:           "User facility + Error (priority 11 = facility 1 * 8 + severity 3)",
			priority:       "11",
			expectedNumber: 17, // SEVERITY_NUMBER_ERROR
			expectedText:   "ERROR",
		},
		{
			name:           "Local0 facility + Warning (priority 132 = facility 16 * 8 + severity 4)",
			priority:       "132",
			expectedNumber: 13, // SEVERITY_NUMBER_WARN
			expectedText:   "WARN",
		},
		{
			name:           "Invalid priority",
			priority:       "invalid",
			expectedNumber: 0,
			expectedText:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNumber, gotText := MapPriorityToSeverity(tt.priority)
			if gotNumber != tt.expectedNumber {
				t.Errorf("MapPriorityToSeverity(%q) number = %d, want %d", tt.priority, gotNumber, tt.expectedNumber)
			}
			if gotText != tt.expectedText {
				t.Errorf("MapPriorityToSeverity(%q) text = %q, want %q", tt.priority, gotText, tt.expectedText)
			}
		})
	}
}

func TestDoubleWrappedBSD(t *testing.T) {
	// This tests the scenario where storage1 sends double-wrapped syslog
	// Outer: RFC3164 format
	// Inner: RFC5424 format
	// Example: "Dec 31 10:05:00 storage1 1 2025-12-31T10:05:00-05:00 storage1 smartd 1501 - - Device: /dev/ada0"

	message := "Dec 31 10:05:00 storage1 1 2025-12-31T10:05:00-05:00 storage1.local smartd 1501 - - Device: /dev/ada0"

	// First parse as RFC3164
	info := Parse(message)
	if info == nil {
		t.Fatal("Expected RFC3164 parse to succeed")
	}
	if info.Format != FormatRFC3164 {
		t.Errorf("Expected FormatRFC3164, got %v", info.Format)
	}

	// Check if content is embedded RFC5424
	if !IsEmbeddedRFC5424(info.Content) {
		t.Fatal("Expected content to be detected as embedded RFC5424")
	}

	// Parse the embedded RFC5424
	embeddedInfo := Parse(info.Content)
	if embeddedInfo == nil {
		t.Fatal("Expected embedded RFC5424 parse to succeed")
	}

	// Verify embedded fields
	if embeddedInfo.Hostname != "storage1.local" {
		t.Errorf("Embedded hostname = %q, want %q", embeddedInfo.Hostname, "storage1.local")
	}
	if embeddedInfo.Appname != "smartd" {
		t.Errorf("Embedded appname = %q, want %q", embeddedInfo.Appname, "smartd")
	}
	if embeddedInfo.ProcID != "1501" {
		t.Errorf("Embedded procid = %q, want %q", embeddedInfo.ProcID, "1501")
	}
	if embeddedInfo.Content != "Device: /dev/ada0" {
		t.Errorf("Embedded content = %q, want %q", embeddedInfo.Content, "Device: /dev/ada0")
	}
}
