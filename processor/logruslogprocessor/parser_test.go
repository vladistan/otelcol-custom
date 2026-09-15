// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logruslogprocessor

import (
	"testing"
	"time"
)

func TestIsLogrusJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "standard logrus JSON",
			input:    `{"level":"info","msg":"Database updated: 8 nodes, 85 pods","time":"2025-12-31T04:03:58Z"}`,
			expected: true,
		},
		{
			name:     "logrus with message field",
			input:    `{"level":"error","message":"Connection failed","time":"2025-12-31T04:03:58Z"}`,
			expected: true,
		},
		{
			name:     "logrus with extra fields",
			input:    `{"level":"debug","msg":"Processing request","time":"2025-12-31T04:03:58Z","request_id":"abc123"}`,
			expected: true,
		},
		{
			name:     "logrus with capitalized fields",
			input:    `{"Level":"Info","Msg":"Starting server","Time":"2025-12-31T04:03:58Z"}`,
			expected: true,
		},
		{
			name:     "zap-style JSON with msg",
			input:    `{"level":"info","msg":"Server started","ts":"2025-12-31T04:03:58Z"}`,
			expected: true,
		},
		{
			name:     "zap-style JSON without msg (controller-runtime)",
			input:    `{"caller":"service_controller.go:64","controller":"ServiceReconciler","level":"info","start reconcile":"opentelemetry/otel-main","ts":"2025-12-31T04:11:19Z"}`,
			expected: true,
		},
		{
			name:     "zap-style error with stacktrace",
			input:    `{"level":"error","ts":"2025-12-21T13:55:03Z","msg":"Reconciler error","controller":"servicel2status","error":"some error","stacktrace":"..."}`,
			expected: true,
		},
		{
			name:     "knative/tekton with severity field",
			input:    `{"severity":"error","timestamp":"2025-12-31T12:50:40.425Z","logger":"tekton-pipelines-webhook","caller":"controller.go:566","message":"Reconcile error","error":"custom resource isn't configured"}`,
			expected: true,
		},
		{
			name:     "json without level - not logrus",
			input:    `{"msg":"Database updated","time":"2025-12-31T04:03:58Z"}`,
			expected: false,
		},
		{
			name:     "json without msg or timestamp - not logrus",
			input:    `{"level":"info","other":"value"}`,
			expected: false,
		},
		{
			name:     "nginx log - not logrus",
			input:    `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582`,
			expected: false,
		},
		{
			name:     "uvicorn log - not logrus",
			input:    `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`,
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "non-json",
			input:    "Just some text",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLogrusJSON(tt.input)
			if result != tt.expected {
				t.Errorf("IsLogrusJSON(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseLogrusJSON(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		expectNil          bool
		expectedLevel      string
		expectedMessage    string
		expectedTime       string
		expectedCaller     string
		expectedController string
		expectedError      string
		expectedExtra      map[string]string
	}{
		{
			name:            "standard logrus JSON",
			input:           `{"level":"info","msg":"Database updated: 8 nodes, 85 pods","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "Database updated: 8 nodes, 85 pods",
			expectedTime:    "2025-12-31T04:03:58Z",
		},
		{
			name:            "logrus with message field",
			input:           `{"level":"error","message":"Connection failed","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "error",
			expectedMessage: "Connection failed",
		},
		{
			name:            "logrus with extra fields",
			input:           `{"level":"debug","msg":"Processing request","time":"2025-12-31T04:03:58Z","request_id":"abc123","user":"admin"}`,
			expectNil:       false,
			expectedLevel:   "debug",
			expectedMessage: "Processing request",
			expectedExtra:   map[string]string{"request_id": "abc123", "user": "admin"},
		},
		{
			name:            "warning level",
			input:           `{"level":"warning","msg":"Resource low","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "warning",
			expectedMessage: "Resource low",
		},
		{
			name:            "warn level (short form)",
			input:           `{"level":"warn","msg":"Deprecated API","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "warn",
			expectedMessage: "Deprecated API",
		},
		{
			name:            "fatal level",
			input:           `{"level":"fatal","msg":"Server crashed","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "fatal",
			expectedMessage: "Server crashed",
		},
		{
			name:            "panic level",
			input:           `{"level":"panic","msg":"Unrecoverable error","time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "panic",
			expectedMessage: "Unrecoverable error",
		},
		{
			name:            "capitalized fields",
			input:           `{"Level":"Info","Msg":"Starting server","Time":"2025-12-31T04:03:58Z"}`,
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "Starting server",
		},
		{
			name:               "zap controller-runtime with msg",
			input:              `{"level":"error","ts":"2025-12-21T13:55:03Z","msg":"Reconciler error","controller":"servicel2status","error":"Value is immutable","stacktrace":"..."}`,
			expectNil:          false,
			expectedLevel:      "error",
			expectedMessage:    "Reconciler error",
			expectedController: "servicel2status",
			expectedError:      "Value is immutable",
		},
		{
			name:               "zap controller-runtime without msg (start reconcile)",
			input:              `{"caller":"service_controller.go:64","controller":"ServiceReconciler","level":"info","start reconcile":"opentelemetry/otel-main","ts":"2025-12-31T04:11:19Z"}`,
			expectNil:          false,
			expectedLevel:      "info",
			expectedMessage:    "start reconcile: opentelemetry/otel-main",
			expectedCaller:     "service_controller.go:64",
			expectedController: "ServiceReconciler",
		},
		{
			name:               "zap controller-runtime without msg (end reconcile)",
			input:              `{"caller":"service_controller.go:115","controller":"ServiceReconciler","end reconcile":"opentelemetry/otel-main","level":"info","ts":"2025-12-31T04:11:19Z"}`,
			expectNil:          false,
			expectedLevel:      "info",
			expectedMessage:    "end reconcile: opentelemetry/otel-main",
			expectedCaller:     "service_controller.go:115",
			expectedController: "ServiceReconciler",
		},
		{
			name:      "invalid JSON",
			input:     `{"level":"info","msg":"broken`,
			expectNil: true,
		},
		{
			name:      "non-json string",
			input:     "Not JSON at all",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseLogrusJSON(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseLogrusJSON(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseLogrusJSON(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}

			if tt.expectedTime != "" && !result.Timestamp.IsZero() {
				expectedTime, _ := time.Parse(time.RFC3339, tt.expectedTime)
				if !result.Timestamp.Equal(expectedTime) {
					t.Errorf("Timestamp = %v, want %v", result.Timestamp, expectedTime)
				}
			}

			if tt.expectedCaller != "" {
				if result.Caller != tt.expectedCaller {
					t.Errorf("Caller = %q, want %q", result.Caller, tt.expectedCaller)
				}
			}

			if tt.expectedController != "" {
				if result.Controller != tt.expectedController {
					t.Errorf("Controller = %q, want %q", result.Controller, tt.expectedController)
				}
			}

			if tt.expectedError != "" {
				if result.Error != tt.expectedError {
					t.Errorf("Error = %q, want %q", result.Error, tt.expectedError)
				}
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

func TestSeverityFromLevel(t *testing.T) {
	tests := []struct {
		level        string
		expectedNum  int
		expectedText string
	}{
		{"trace", 1, "TRACE"},
		{"debug", 5, "DEBUG"},
		{"info", 9, "INFO"},
		{"warn", 13, "WARN"},
		{"warning", 13, "WARN"},
		{"error", 17, "ERROR"},
		{"fatal", 21, "FATAL"},
		{"panic", 21, "FATAL"},
		{"INFO", 9, "INFO"},     // uppercase
		{"Warning", 13, "WARN"}, // mixed case
		{"unknown", 9, "INFO"},  // unknown defaults to INFO
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			num, text := SeverityFromLevel(tt.level)
			if num != tt.expectedNum {
				t.Errorf("SeverityFromLevel(%q) num = %d, want %d", tt.level, num, tt.expectedNum)
			}
			if text != tt.expectedText {
				t.Errorf("SeverityFromLevel(%q) text = %q, want %q", tt.level, text, tt.expectedText)
			}
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input    string
		expected string // empty means zero time
	}{
		{"2025-12-31T04:03:58Z", "2025-12-31T04:03:58Z"},
		{"2025-12-31T04:03:58.123Z", "2025-12-31T04:03:58.123Z"},
		{"2025-12-31T04:03:58.123456Z", "2025-12-31T04:03:58.123456Z"},
		{"invalid", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseTimestamp(tt.input)
			if tt.expected == "" {
				if !result.IsZero() {
					t.Errorf("parseTimestamp(%q) = %v, want zero time", tt.input, result)
				}
			} else {
				expected, _ := time.Parse(time.RFC3339Nano, tt.expected)
				if !result.Equal(expected) {
					t.Errorf("parseTimestamp(%q) = %v, want %v", tt.input, result, expected)
				}
			}
		})
	}
}

func TestIsLogrusText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "standard logrus text format",
			input:    `time="2025-12-31T04:25:29Z" level=info msg="All records are already up to date"`,
			expected: true,
		},
		{
			name:     "logrus text with extra fields",
			input:    `time="2025-12-31T04:26:30Z" level=info msg="Applying provider record filter for domains: [r4.example.com.]" source=external-dns`,
			expected: true,
		},
		{
			name:     "logrus text error level",
			input:    `time="2025-12-31T04:25:29Z" level=error msg="Failed to connect" error="connection refused"`,
			expected: true,
		},
		{
			name:     "logrus text warning level",
			input:    `time="2025-12-31T04:25:29Z" level=warning msg="Deprecated feature used"`,
			expected: true,
		},
		{
			name:     "logrus JSON - not text",
			input:    `{"level":"info","msg":"message","time":"2025-12-31T04:03:58Z"}`,
			expected: false,
		},
		{
			name:     "nginx log - not logrus",
			input:    `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582`,
			expected: false,
		},
		{
			name:     "uvicorn log - not logrus",
			input:    `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLogrusText(tt.input)
			if result != tt.expected {
				t.Errorf("IsLogrusText(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseLogrusText(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectNil       bool
		expectedLevel   string
		expectedMessage string
		expectedTime    string
		expectedExtra   map[string]string
	}{
		{
			name:            "standard logrus text",
			input:           `time="2025-12-31T04:25:29Z" level=info msg="All records are already up to date"`,
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "All records are already up to date",
			expectedTime:    "2025-12-31T04:25:29Z",
		},
		{
			name:            "logrus text with extra fields",
			input:           `time="2025-12-31T04:26:30Z" level=info msg="Applying filter" source="external-dns" zone="r4.example.com"`,
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "Applying filter",
			expectedExtra:   map[string]string{"source": "external-dns", "zone": "r4.example.com"},
		},
		{
			name:            "logrus text error level",
			input:           `time="2025-12-31T04:25:29Z" level=error msg="Connection failed"`,
			expectNil:       false,
			expectedLevel:   "error",
			expectedMessage: "Connection failed",
		},
		{
			name:            "logrus text warning level",
			input:           `time="2025-12-31T04:25:29Z" level=warning msg="Deprecated API"`,
			expectNil:       false,
			expectedLevel:   "warning",
			expectedMessage: "Deprecated API",
		},
		{
			name:      "not logrus text format",
			input:     `Just some plain text`,
			expectNil: true,
		},
		{
			name:      "logrus JSON - not text",
			input:     `{"level":"info","msg":"message","time":"2025-12-31T04:03:58Z"}`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseLogrusText(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseLogrusText(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseLogrusText(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}

			if tt.expectedTime != "" && !result.Timestamp.IsZero() {
				expectedTime, _ := time.Parse(time.RFC3339, tt.expectedTime)
				if !result.Timestamp.Equal(expectedTime) {
					t.Errorf("Timestamp = %v, want %v", result.Timestamp, expectedTime)
				}
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

func TestIsZapConsole(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "otelcol-edge info log",
			input:    "2026-01-02T14:18:43.362Z\tinfo\tinternal/retry_sender.go:133\tExporting failed. Will retry the request after interval.\t{\"resource\": {}, \"error\": \"connection refused\"}",
			expected: true,
		},
		{
			name:     "otelcol-edge warn log",
			input:    "2026-01-02T14:18:43.362Z\twarn\tservice/service.go:100\tSome warning message",
			expected: true,
		},
		{
			name:     "otelcol-edge error log",
			input:    "2026-01-02T14:18:43.362Z\terror\texporter/exporter.go:50\tFailed to send",
			expected: true,
		},
		{
			name:     "otelcol-edge debug log",
			input:    "2026-01-02T14:18:43.362Z\tdebug\treceiver/receiver.go:25\tReceived data",
			expected: true,
		},
		{
			name:     "otelcol-edge fatal log",
			input:    "2026-01-02T14:18:43.362Z\tfatal\tmain.go:50\tCritical failure",
			expected: true,
		},
		{
			name:     "otelcol-edge dpanic log",
			input:    "2026-01-02T14:18:43.362Z\tdpanic\thandler.go:100\tDevelopment panic",
			expected: true,
		},
		{
			name:     "timestamp without milliseconds",
			input:    "2026-01-02T14:18:43Z\tinfo\tfile.go:10\tMessage",
			expected: true,
		},
		{
			name:     "logrus JSON - not zap console",
			input:    "{\"level\":\"info\",\"msg\":\"message\",\"time\":\"2025-12-31T04:03:58Z\"}",
			expected: false,
		},
		{
			name:     "logrus text - not zap console",
			input:    "time=\"2025-12-31T04:25:29Z\" level=info msg=\"message\"",
			expected: false,
		},
		{
			name:     "nginx log - not zap console",
			input:    "10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] \"GET /healthz/ HTTP/1.1\" 200 582",
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
			name:     "invalid level - not zap console",
			input:    "2026-01-02T14:18:43.362Z\tinvalid\tfile.go:10\tMessage",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsZapConsole(tt.input)
			if result != tt.expected {
				t.Errorf("IsZapConsole(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseZapConsole(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectNil       bool
		expectedLevel   string
		expectedMessage string
		expectedCaller  string
		expectedError   string
		expectedExtra   map[string]string
	}{
		{
			name:            "otelcol-edge info log with JSON context",
			input:           "2026-01-02T14:18:43.362Z\tinfo\tinternal/retry_sender.go:133\tExporting failed. Will retry the request after interval.\t{\"otelcol.component.id\": \"otlphttp\", \"otelcol.signal\": \"metrics\", \"error\": \"connection refused\", \"interval\": \"5.5s\"}",
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "Exporting failed. Will retry the request after interval.",
			expectedCaller:  "internal/retry_sender.go:133",
			expectedError:   "connection refused",
			expectedExtra:   map[string]string{"otelcol.component.id": "otlphttp", "otelcol.signal": "metrics", "interval": "5.5s"},
		},
		{
			name:            "otelcol-edge warn log without JSON",
			input:           "2026-01-02T14:18:43.362Z\twarn\tservice/service.go:100\tConfiguration warning",
			expectNil:       false,
			expectedLevel:   "warn",
			expectedMessage: "Configuration warning",
			expectedCaller:  "service/service.go:100",
		},
		{
			name:            "otelcol-edge error log",
			input:           "2026-01-02T14:18:43.362Z\terror\texporter/exporter.go:50\tFailed to send batch\t{\"error\": \"timeout\"}",
			expectNil:       false,
			expectedLevel:   "error",
			expectedMessage: "Failed to send batch",
			expectedCaller:  "exporter/exporter.go:50",
			expectedError:   "timeout",
		},
		{
			name:            "simple info log",
			input:           "2026-01-02T14:18:43Z\tinfo\tservice@v0.140.0/service.go:247\tEverything is ready. Begin running and processing data.",
			expectNil:       false,
			expectedLevel:   "info",
			expectedMessage: "Everything is ready. Begin running and processing data.",
			expectedCaller:  "service@v0.140.0/service.go:247",
		},
		{
			name:      "not zap console format",
			input:     "{\"level\":\"info\",\"msg\":\"message\"}",
			expectNil: true,
		},
		{
			name:      "too few parts",
			input:     "2026-01-02T14:18:43.362Z\tinfo\tfile.go:10",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseZapConsole(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseZapConsole(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseZapConsole(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}

			if tt.expectedCaller != "" && result.Caller != tt.expectedCaller {
				t.Errorf("Caller = %q, want %q", result.Caller, tt.expectedCaller)
			}

			if tt.expectedError != "" && result.Error != tt.expectedError {
				t.Errorf("Error = %q, want %q", result.Error, tt.expectedError)
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
