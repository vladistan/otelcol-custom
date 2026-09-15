// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klogprocessor

import (
	"testing"
)

func TestIsKlog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "info log",
			input:    `I1231 12:28:10.685217       1 watcher.go:338] watch chan error: etcdserver: mvcc: required revision has been compacted`,
			expected: true,
		},
		{
			name:     "warning log",
			input:    `W1231 12:28:10.685217       1 watcher.go:338] watch chan error`,
			expected: true,
		},
		{
			name:     "error log",
			input:    `E1231 12:00:04.713376       1 cronjob_controllerv2.go:174] "Unhandled Error" err="error syncing"`,
			expected: true,
		},
		{
			name:     "fatal log",
			input:    `F1231 12:00:04.713376       1 main.go:10] fatal error`,
			expected: true,
		},
		{
			name:     "info with structured logging",
			input:    `I1231 12:25:21.569408       1 cidrallocator.go:277] updated ClusterIP allocator for Service CIDR fd85:ee78:d8a6:8600::1000/116`,
			expected: true,
		},
		{
			name:     "logrus JSON - not klog",
			input:    `{"level":"info","msg":"message","time":"2025-12-31T04:03:58Z"}`,
			expected: false,
		},
		{
			name:     "logrus text - not klog",
			input:    `time="2025-12-31T04:25:29Z" level=info msg="All records are already up to date"`,
			expected: false,
		},
		{
			name:     "nginx log - not klog",
			input:    `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582`,
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "plain text",
			input:    "Just some text",
			expected: false,
		},
		{
			name:     "starts with I but not klog",
			input:    "INFO: some message",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsKlog(tt.input)
			if result != tt.expected {
				t.Errorf("IsKlog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseKlog(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectNil       bool
		expectedLevel   string
		expectedMessage string
		expectedFile    string
		expectedLine    string
		expectedExtra   map[string]string
	}{
		{
			name:            "simple info log",
			input:           `I1231 12:28:10.685217       1 watcher.go:338] watch chan error: etcdserver: mvcc: required revision has been compacted`,
			expectNil:       false,
			expectedLevel:   "I",
			expectedMessage: "watch chan error: etcdserver: mvcc: required revision has been compacted",
			expectedFile:    "watcher.go",
			expectedLine:    "338",
		},
		{
			name:            "warning log",
			input:           `W1231 12:28:10.685217       1 watcher.go:338] watch chan error`,
			expectNil:       false,
			expectedLevel:   "W",
			expectedMessage: "watch chan error",
			expectedFile:    "watcher.go",
			expectedLine:    "338",
		},
		{
			name:            "structured error log",
			input:           `E1231 12:00:04.713376       1 cronjob_controllerv2.go:174] "Unhandled Error" err="error syncing CronJob" logger="UnhandledError"`,
			expectNil:       false,
			expectedLevel:   "E",
			expectedMessage: "Unhandled Error",
			expectedFile:    "cronjob_controllerv2.go",
			expectedLine:    "174",
			expectedExtra:   map[string]string{"err": "error syncing CronJob", "logger": "UnhandledError"},
		},
		{
			name:            "info log with CIDR",
			input:           `I1231 12:25:21.569408       1 cidrallocator.go:277] updated ClusterIP allocator for Service CIDR fd85:ee78:d8a6:8600::1000/116`,
			expectNil:       false,
			expectedLevel:   "I",
			expectedMessage: "updated ClusterIP allocator for Service CIDR fd85:ee78:d8a6:8600::1000/116",
			expectedFile:    "cidrallocator.go",
			expectedLine:    "277",
		},
		{
			name:      "not klog format",
			input:     `Just some plain text`,
			expectNil: true,
		},
		{
			name:      "logrus JSON",
			input:     `{"level":"info","msg":"message"}`,
			expectNil: true,
		},
		{
			name:            "kubelet probe failed",
			input:           `I1220 03:18:01.710490    1233 prober.go:120] "Probe failed" probeType="Readiness" pod="kube-system/kube-apiserver-k8s-0" podUID="040476ba7586efa8b66c57331451b5fa" containerName="kube-apiserver" probeResult="failure" output="HTTP probe failed with statuscode: 500"`,
			expectNil:       false,
			expectedLevel:   "I",
			expectedMessage: "Probe failed",
			expectedFile:    "prober.go",
			expectedLine:    "120",
			expectedExtra: map[string]string{
				"probeType":     "Readiness",
				"pod":           "kube-system/kube-apiserver-k8s-0",
				"podUID":        "040476ba7586efa8b66c57331451b5fa",
				"containerName": "kube-apiserver",
				"probeResult":   "failure",
			},
		},
		{
			name:            "kubelet SyncLoop probe",
			input:           `I0102 05:00:33.603375    1344 kubelet.go:2643] "SyncLoop (probe)" probe="readiness" status="ready" pod="taxtime/backend-8478b5b58b-8v7ds"`,
			expectNil:       false,
			expectedLevel:   "I",
			expectedMessage: "SyncLoop (probe)",
			expectedFile:    "kubelet.go",
			expectedLine:    "2643",
			expectedExtra: map[string]string{
				"probe":  "readiness",
				"status": "ready",
				"pod":    "taxtime/backend-8478b5b58b-8v7ds",
			},
		},
		{
			name:            "kubelet container finished",
			input:           `I0102 05:00:25.573798    1344 generic.go:358] "Generic (PLEG): container finished" podID="3eb0f5ea-a5fe-4309-973b-eb5fe856f853" containerID="af91019d02c2b507a4c7c12d36aa7d8a171e460d459dca7c84a187cd56e72cb9" exitCode=0`,
			expectNil:       false,
			expectedLevel:   "I",
			expectedMessage: "Generic (PLEG): container finished",
			expectedFile:    "generic.go",
			expectedLine:    "358",
			expectedExtra: map[string]string{
				"podID":       "3eb0f5ea-a5fe-4309-973b-eb5fe856f853",
				"containerID": "af91019d02c2b507a4c7c12d36aa7d8a171e460d459dca7c84a187cd56e72cb9",
				"exitCode":    "0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseKlog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseKlog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseKlog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}

			if result.File != tt.expectedFile {
				t.Errorf("File = %q, want %q", result.File, tt.expectedFile)
			}

			if result.Line != tt.expectedLine {
				t.Errorf("Line = %q, want %q", result.Line, tt.expectedLine)
			}

			if tt.expectedExtra != nil {
				for k, v := range tt.expectedExtra {
					if result.Extra[k] != v {
						t.Errorf("Extra[%q] = %q, want %q", k, result.Extra[k], v)
					}
				}
			}
		})
	}
}

func TestSeverityFromKlogLevel(t *testing.T) {
	tests := []struct {
		level        string
		expectedNum  int
		expectedText string
	}{
		{"I", 9, "INFO"},
		{"W", 13, "WARN"},
		{"E", 17, "ERROR"},
		{"F", 21, "FATAL"},
		{"X", 9, "INFO"}, // unknown defaults to INFO
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			num, text := SeverityFromKlogLevel(tt.level)
			if num != tt.expectedNum {
				t.Errorf("SeverityFromKlogLevel(%q) num = %d, want %d", tt.level, num, tt.expectedNum)
			}
			if text != tt.expectedText {
				t.Errorf("SeverityFromKlogLevel(%q) text = %q, want %q", tt.level, text, tt.expectedText)
			}
		})
	}
}
