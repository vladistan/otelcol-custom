// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sentrylogprocessor

import (
	"testing"
)

func TestIsSentryLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid sentry access log",
			input:    "02:30:21 [INFO] sentry.access.api: api.access (method='GET' view='sentry.web.frontend.home.HomeView' response='302')",
			expected: true,
		},
		{
			name:     "valid sentry warning log",
			input:    "14:25:33 [WARNING] sentry.tasks: Task failed retrying",
			expected: true,
		},
		{
			name:     "valid sentry error log",
			input:    "09:15:00 [ERROR] sentry.celery: Worker crashed",
			expected: true,
		},
		{
			name:     "nginx combined log",
			input:    `172.31.1.225 - - [31/Dec/2025:02:30:21 +0000] "GET / HTTP/1.1" 302 0 "-" "ELB-HealthChecker/2.0" "-"`,
			expected: false,
		},
		{
			name:     "pgbouncer log",
			input:    "2025-12-31 02:30:17.889 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@127.0.0.1:55448 login attempt",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "short string",
			input:    "hello",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSentryLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsSentryLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseSentryLog(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectNil      bool
		expectedLevel  string
		expectedLogger string
		expectedMethod string
		expectedPath   string
		expectedResp   string
	}{
		{
			name:           "access log with all fields",
			input:          "02:30:21 [INFO] sentry.access.api: api.access (method='GET' view='sentry.web.frontend.home.HomeView' response='302' is_frontend_request='False' path='/' caller_ip='172.31.1.225' user_agent='ELB-HealthChecker/2.0' rate_limited='False' request_duration_seconds='0.0029540061950683594' rate_limit_type='DNE')",
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedLogger: "sentry.access.api",
			expectedMethod: "GET",
			expectedPath:   "/",
			expectedResp:   "302",
		},
		{
			name:           "simple warning log",
			input:          "14:25:33 [WARNING] sentry.tasks: task.retry (task='process_event' retries='3')",
			expectNil:      false,
			expectedLevel:  "WARNING",
			expectedLogger: "sentry.tasks",
		},
		{
			name:           "error log",
			input:          "09:15:00 [ERROR] sentry.celery: worker.crashed (reason='OOM')",
			expectNil:      false,
			expectedLevel:  "ERROR",
			expectedLogger: "sentry.celery",
		},
		{
			name:           "debug log",
			input:          "12:00:00 [DEBUG] sentry.cache: cache.hit (key='user:123')",
			expectNil:      false,
			expectedLevel:  "DEBUG",
			expectedLogger: "sentry.cache",
		},
		{
			name:           "simple message without key-value",
			input:          "10:30:45 [INFO] sentry.startup: Server started successfully",
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedLogger: "sentry.startup",
		},
		{
			name:      "not a sentry log",
			input:     "Just some random text",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSentryLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseSentryLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseSentryLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Logger != tt.expectedLogger {
				t.Errorf("Logger = %q, want %q", result.Logger, tt.expectedLogger)
			}

			if tt.expectedMethod != "" && result.Method != tt.expectedMethod {
				t.Errorf("Method = %q, want %q", result.Method, tt.expectedMethod)
			}

			if tt.expectedPath != "" && result.Path != tt.expectedPath {
				t.Errorf("Path = %q, want %q", result.Path, tt.expectedPath)
			}

			if tt.expectedResp != "" && result.Response != tt.expectedResp {
				t.Errorf("Response = %q, want %q", result.Response, tt.expectedResp)
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
		{"DEBUG", 5, "DEBUG"},
		{"debug", 5, "DEBUG"},
		{"INFO", 9, "INFO"},
		{"info", 9, "INFO"},
		{"WARNING", 13, "WARN"},
		{"warning", 13, "WARN"},
		{"WARN", 13, "WARN"},
		{"ERROR", 17, "ERROR"},
		{"error", 17, "ERROR"},
		{"CRITICAL", 21, "FATAL"},
		{"FATAL", 21, "FATAL"},
		{"unknown", 9, "INFO"},
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

func TestBuildCleanedMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *SentryLogInfo
		expected string
	}{
		{
			name: "HTTP request with all fields",
			info: &SentryLogInfo{
				Method:   "GET",
				Path:     "/api/users",
				Response: "200",
				Duration: "0.123",
			},
			expected: "200 GET /api/users",
		},
		{
			name: "HTTP request POST",
			info: &SentryLogInfo{
				Method:   "POST",
				Path:     "/api/events",
				Response: "201",
			},
			expected: "201 POST /api/events",
		},
		{
			name: "fallback to message type when missing fields",
			info: &SentryLogInfo{
				MessageType: "task.completed",
			},
			expected: "task.completed",
		},
		{
			name: "fallback when missing response",
			info: &SentryLogInfo{
				Method:      "GET",
				Path:        "/api/test",
				MessageType: "api.access",
			},
			expected: "api.access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCleanedMessage(tt.info)
			if result != tt.expected {
				t.Errorf("buildCleanedMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestAccessLogParsing(t *testing.T) {
	// Real example from sentry-self-hosted-web-1
	input := "02:30:21 [INFO] sentry.access.api: api.access (method='GET' view='sentry.web.frontend.home.HomeView' response='302' is_frontend_request='False' path='/' caller_ip='172.31.1.225' user_agent='ELB-HealthChecker/2.0' rate_limited='False' request_duration_seconds='0.0029540061950683594' rate_limit_type='DNE')"

	info := ParseSentryLog(input)
	if info == nil {
		t.Fatal("ParseSentryLog returned nil")
	}

	// Verify all extracted fields
	if info.Level != "INFO" {
		t.Errorf("Level = %q, want INFO", info.Level)
	}
	if info.Logger != "sentry.access.api" {
		t.Errorf("Logger = %q, want sentry.access.api", info.Logger)
	}
	if info.MessageType != "api.access" {
		t.Errorf("MessageType = %q, want api.access", info.MessageType)
	}
	if info.Method != "GET" {
		t.Errorf("Method = %q, want GET", info.Method)
	}
	if info.Path != "/" {
		t.Errorf("Path = %q, want /", info.Path)
	}
	if info.Response != "302" {
		t.Errorf("Response = %q, want 302", info.Response)
	}
	if info.CallerIP != "172.31.1.225" {
		t.Errorf("CallerIP = %q, want 172.31.1.225", info.CallerIP)
	}
	if info.UserAgent != "ELB-HealthChecker/2.0" {
		t.Errorf("UserAgent = %q, want ELB-HealthChecker/2.0", info.UserAgent)
	}
	if info.Duration != "0.0029540061950683594" {
		t.Errorf("Duration = %q, want 0.0029540061950683594", info.Duration)
	}
	if info.RateLimited != "False" {
		t.Errorf("RateLimited = %q, want False", info.RateLimited)
	}
	if info.View != "sentry.web.frontend.home.HomeView" {
		t.Errorf("View = %q, want sentry.web.frontend.home.HomeView", info.View)
	}

	// Verify cleaned message: {status_code} {method} {path}
	expectedMsg := "302 GET /"
	if info.Message != expectedMsg {
		t.Errorf("Message = %q, want %q", info.Message, expectedMsg)
	}
}
