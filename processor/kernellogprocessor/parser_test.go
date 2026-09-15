// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kernellogprocessor

import (
	"testing"
)

func TestIsKernelLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "SCSI error",
			input:    `sd 11:0:0:0: [sdd] tag#0 FAILED Result: hostbyte=DID_OK driverbyte=DRIVER_OK cmd_age=0s`,
			expected: true,
		},
		{
			name:     "critical medium error",
			input:    `critical medium error, dev sdd, sector 0 op 0x0:(READ) flags 0x0 phys_seg 32 prio class 2`,
			expected: true,
		},
		{
			name:     "IPVS no destination",
			input:    `IPVS: rr: TCP 192.0.2.1:4318 - no destination available`,
			expected: true,
		},
		{
			name:     "net ratelimit",
			input:    `net_ratelimit: 21 callbacks suppressed`,
			expected: true,
		},
		{
			name:     "cni port state",
			input:    `cni0: port 6(veth5eff7967) entered blocking state`,
			expected: true,
		},
		{
			name:     "veth promiscuous",
			input:    `veth1e60a14b: entered promiscuous mode`,
			expected: true,
		},
		{
			name:     "BSD kernel timestamp",
			input:    `[230890] em0: promiscuous mode disabled`,
			expected: true,
		},
		{
			name:     "clocksource",
			input:    `clocksource: Long readout interval, skipping watchdog check: cs_nsec: 1859072985 wd_nsec: 1859072414`,
			expected: true,
		},
		{
			name:     "strongSwan IPsec",
			input:    `14[NET] <efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|1> sending packet: from 24.2.177.2[500] to 3.212.18.114[500] (1164 bytes)`,
			expected: true,
		},
		{
			name:     "network interface",
			input:    `em0: promiscuous mode enabled`,
			expected: true,
		},
		{
			name:     "not kernel - JSON",
			input:    `{"level":"info","msg":"message","time":"2025-12-31T04:03:58Z"}`,
			expected: false,
		},
		{
			name:     "not kernel - klog",
			input:    `I1231 12:28:10.685217       1 watcher.go:338] watch chan error`,
			expected: false,
		},
		{
			name:     "not kernel - nginx",
			input:    `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582`,
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "short string",
			input:    "ab",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsKernelLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsKernelLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseKernelLog(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedSubsystem string
		expectedDevice    string
		expectedSeverity  string
		expectedErrorType string
		expectedMessage   string
		expectedExtra     map[string]string
	}{
		{
			name:              "SCSI error with sense key",
			input:             `sd 11:0:0:0: [sdd] tag#0 FAILED Result: hostbyte=DID_OK driverbyte=DRIVER_OK`,
			expectedSubsystem: "scsi",
			expectedDevice:    "sdd",
			expectedSeverity:  "ERROR",
			expectedErrorType: "SCSI Command Failed",
			expectedExtra:     map[string]string{"scsi.address": "11:0:0:0"},
		},
		{
			name:              "critical medium error",
			input:             `critical medium error, dev sdd, sector 2048 op 0x0:(READ)`,
			expectedSubsystem: "block",
			expectedDevice:    "sdd",
			expectedSeverity:  "ERROR",
			expectedErrorType: "Critical Medium Error",
			expectedMessage:   "Critical medium error on sdd sector 2048",
			expectedExtra:     map[string]string{"block.sector": "2048"},
		},
		{
			name:              "IPVS no destination",
			input:             `IPVS: rr: TCP 192.0.2.1:4318 - no destination available`,
			expectedSubsystem: "ipvs",
			expectedSeverity:  "WARN",
			expectedErrorType: "No Destination",
			expectedExtra:     map[string]string{"ipvs.scheduler": "rr", "ipvs.protocol": "TCP", "ipvs.destination": "192.0.2.1:4318"},
		},
		{
			name:              "net ratelimit",
			input:             `net_ratelimit: 21 callbacks suppressed`,
			expectedSubsystem: "net",
			expectedMessage:   "21 network callbacks suppressed",
			expectedExtra:     map[string]string{"net.suppressed_count": "21"},
		},
		{
			name:              "BSD kernel with timestamp",
			input:             `[230890] em0: promiscuous mode disabled`,
			expectedSubsystem: "net",
			expectedDevice:    "em0",
			expectedExtra:     map[string]string{"kernel.uptime": "230890", "net.promiscuous": "disabled"},
		},
		{
			name:              "cni port blocking",
			input:             `cni0: port 6(veth5eff7967) entered blocking state`,
			expectedSubsystem: "net",
			expectedDevice:    "cni0",
			expectedExtra:     map[string]string{"net.port_state": "blocking"},
		},
		{
			name:              "veth promiscuous mode",
			input:             `veth1e60a14b: entered promiscuous mode`,
			expectedSubsystem: "net",
			expectedDevice:    "veth1e60a14b",
		},
		{
			name:              "clocksource",
			input:             `clocksource: Long readout interval, skipping watchdog check`,
			expectedSubsystem: "clocksource",
			expectedMessage:   "Clocksource: Long readout interval, skipping watchdog check",
		},
		{
			name:              "strongSwan IPsec",
			input:             `14[NET] <efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|1> sending packet: from 24.2.177.2[500] to 3.212.18.114[500]`,
			expectedSubsystem: "ipsec",
			expectedExtra:     map[string]string{"ipsec.component": "NET", "ipsec.connection": "efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseKernelLog(tt.input)
			if result == nil {
				t.Fatalf("ParseKernelLog(%q) returned nil", tt.input)
			}

			if tt.expectedSubsystem != "" && result.Subsystem != tt.expectedSubsystem {
				t.Errorf("Subsystem = %q, want %q", result.Subsystem, tt.expectedSubsystem)
			}

			if tt.expectedDevice != "" && result.Device != tt.expectedDevice {
				t.Errorf("Device = %q, want %q", result.Device, tt.expectedDevice)
			}

			if tt.expectedSeverity != "" && result.Severity != tt.expectedSeverity {
				t.Errorf("Severity = %q, want %q", result.Severity, tt.expectedSeverity)
			}

			if tt.expectedErrorType != "" && result.ErrorType != tt.expectedErrorType {
				t.Errorf("ErrorType = %q, want %q", result.ErrorType, tt.expectedErrorType)
			}

			if tt.expectedMessage != "" && result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}

			for k, v := range tt.expectedExtra {
				if result.Extra[k] != v {
					t.Errorf("Extra[%q] = %q, want %q", k, result.Extra[k], v)
				}
			}
		})
	}
}

func TestSeverityFromKernelLog(t *testing.T) {
	tests := []struct {
		severity     string
		expectedNum  int
		expectedText string
	}{
		{"ERROR", 17, "ERROR"},
		{"WARN", 13, "WARN"},
		{"INFO", 9, "INFO"},
		{"", 9, "INFO"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			info := &KernelLogInfo{Severity: tt.severity}
			num, text := SeverityFromKernelLog(info)
			if num != tt.expectedNum {
				t.Errorf("SeverityFromKernelLog() num = %d, want %d", num, tt.expectedNum)
			}
			if text != tt.expectedText {
				t.Errorf("SeverityFromKernelLog() text = %q, want %q", text, tt.expectedText)
			}
		})
	}
}
