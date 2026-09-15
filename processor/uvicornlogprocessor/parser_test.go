// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

import (
	"testing"
)

func TestIsUvicornLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "info level",
			input:    `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`,
			expected: true,
		},
		{
			name:     "warning level",
			input:    `WARNING:  192.168.1.1:8080 - "POST /api/submit HTTP/1.1" 400 Bad Request`,
			expected: true,
		},
		{
			name:     "error level",
			input:    `ERROR:    10.0.0.1 - "GET /api/error HTTP/1.1" 500 Internal Server Error`,
			expected: true,
		},
		{
			name:     "debug level",
			input:    `DEBUG:    127.0.0.1:9000 - "GET /debug HTTP/1.1" 200 OK`,
			expected: true,
		},
		{
			name:     "critical level",
			input:    `CRITICAL: 10.0.0.1:80 - "GET /crash HTTP/1.1" 500 Server Error`,
			expected: true,
		},
		{
			name:     "nginx log - not uvicorn",
			input:    `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582 "-" "kube-probe/1.33" "-"`,
			expected: false,
		},
		{
			name:     "postgres log - not uvicorn",
			input:    `2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key value violates unique constraint`,
			expected: false,
		},
		{
			name:     "kafka log - not uvicorn",
			input:    `[2025-12-31 01:15:07,291] INFO Cleaner 0: Cleaning log (kafka.log.LogCleaner)`,
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "short string",
			input:    "INFO:",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsUvicornLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsUvicornLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseUvicornLog(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectNil      bool
		expectedLevel  string
		expectedIP     string
		expectedPort   string
		expectedMethod string
		expectedPath   string
		expectedQuery  string
		expectedStatus int
		expectedText   string
	}{
		{
			name:           "simple GET request",
			input:          `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedIP:     "10.233.68.104",
			expectedPort:   "55370",
			expectedMethod: "GET",
			expectedPath:   "/api/healthz/",
			expectedStatus: 200,
			expectedText:   "OK",
		},
		{
			name:           "POST with query string",
			input:          `INFO:     192.168.1.1:8080 - "POST /api/search?q=test&limit=10 HTTP/1.1" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedIP:     "192.168.1.1",
			expectedPort:   "8080",
			expectedMethod: "POST",
			expectedPath:   "/api/search",
			expectedQuery:  "q=test&limit=10",
			expectedStatus: 200,
			expectedText:   "OK",
		},
		{
			name:           "warning level 400 error",
			input:          `WARNING:  192.168.1.1:8080 - "POST /api/submit HTTP/1.1" 400 Bad Request`,
			expectNil:      false,
			expectedLevel:  "WARNING",
			expectedIP:     "192.168.1.1",
			expectedPort:   "8080",
			expectedMethod: "POST",
			expectedPath:   "/api/submit",
			expectedStatus: 400,
			expectedText:   "Bad Request",
		},
		{
			name:           "error level 500 error",
			input:          `ERROR:    10.0.0.1:9999 - "GET /api/error HTTP/1.1" 500 Internal Server Error`,
			expectNil:      false,
			expectedLevel:  "ERROR",
			expectedIP:     "10.0.0.1",
			expectedPort:   "9999",
			expectedMethod: "GET",
			expectedPath:   "/api/error",
			expectedStatus: 500,
			expectedText:   "Internal Server Error",
		},
		{
			name:           "client without port",
			input:          `INFO:     10.0.0.1 - "GET /api/test HTTP/1.1" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedIP:     "10.0.0.1",
			expectedPort:   "",
			expectedMethod: "GET",
			expectedPath:   "/api/test",
			expectedStatus: 200,
			expectedText:   "OK",
		},
		{
			name:           "HTTP/2 protocol",
			input:          `INFO:     127.0.0.1:8000 - "GET /api/v2 HTTP/2" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedIP:     "127.0.0.1",
			expectedPort:   "8000",
			expectedMethod: "GET",
			expectedPath:   "/api/v2",
			expectedStatus: 200,
			expectedText:   "OK",
		},
		{
			name:           "DELETE method",
			input:          `INFO:     10.0.0.1:80 - "DELETE /api/item/123 HTTP/1.1" 204`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedIP:     "10.0.0.1",
			expectedPort:   "80",
			expectedMethod: "DELETE",
			expectedPath:   "/api/item/123",
			expectedStatus: 204,
			expectedText:   "",
		},
		{
			name:           "PUT method",
			input:          `INFO:     10.0.0.1:80 - "PUT /api/item/123 HTTP/1.1" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedMethod: "PUT",
			expectedPath:   "/api/item/123",
			expectedStatus: 200,
		},
		{
			name:           "PATCH method",
			input:          `INFO:     10.0.0.1:80 - "PATCH /api/item/123 HTTP/1.1" 200 OK`,
			expectNil:      false,
			expectedLevel:  "INFO",
			expectedMethod: "PATCH",
			expectedPath:   "/api/item/123",
			expectedStatus: 200,
		},
		{
			name:      "not a uvicorn log",
			input:     "Just some random text",
			expectNil: true,
		},
		{
			name:      "nginx log",
			input:     `10.233.67.85 - - [31/Dec/2025:03:56:10 +0000] "GET /healthz/ HTTP/1.1" 200 582`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseUvicornLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseUvicornLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseUvicornLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if tt.expectedIP != "" && result.ClientIP != tt.expectedIP {
				t.Errorf("ClientIP = %q, want %q", result.ClientIP, tt.expectedIP)
			}

			if tt.expectedPort != "" && result.ClientPort != tt.expectedPort {
				t.Errorf("ClientPort = %q, want %q", result.ClientPort, tt.expectedPort)
			}

			if result.Method != tt.expectedMethod {
				t.Errorf("Method = %q, want %q", result.Method, tt.expectedMethod)
			}

			if result.Path != tt.expectedPath {
				t.Errorf("Path = %q, want %q", result.Path, tt.expectedPath)
			}

			if tt.expectedQuery != "" && result.Query != tt.expectedQuery {
				t.Errorf("Query = %q, want %q", result.Query, tt.expectedQuery)
			}

			if result.StatusCode != tt.expectedStatus {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.expectedStatus)
			}

			if tt.expectedText != "" && result.StatusText != tt.expectedText {
				t.Errorf("StatusText = %q, want %q", result.StatusText, tt.expectedText)
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
		{"INFO", 9, "INFO"},
		{"WARNING", 13, "WARN"},
		{"ERROR", 17, "ERROR"},
		{"CRITICAL", 21, "FATAL"},
		{"unknown", 9, "INFO"}, // unknown defaults to INFO
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

func TestSeverityFromStatusCode(t *testing.T) {
	tests := []struct {
		statusCode   int
		expectedNum  int
		expectedText string
	}{
		{200, 9, "INFO"},
		{201, 9, "INFO"},
		{204, 9, "INFO"},
		{301, 9, "INFO"},
		{304, 9, "INFO"},
		{400, 13, "WARN"},
		{401, 13, "WARN"},
		{403, 13, "WARN"},
		{404, 13, "WARN"},
		{500, 17, "ERROR"},
		{502, 17, "ERROR"},
		{503, 17, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.statusCode)), func(t *testing.T) {
			num, text := SeverityFromStatusCode(tt.statusCode)
			if num != tt.expectedNum {
				t.Errorf("SeverityFromStatusCode(%d) num = %d, want %d", tt.statusCode, num, tt.expectedNum)
			}
			if text != tt.expectedText {
				t.Errorf("SeverityFromStatusCode(%d) text = %q, want %q", tt.statusCode, text, tt.expectedText)
			}
		})
	}
}

func TestBuildCleanedMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *UvicornLogInfo
		expected string
	}{
		{
			name: "simple GET",
			info: &UvicornLogInfo{
				StatusCode: 200,
				Method:     "GET",
				Path:       "/api/healthz/",
			},
			expected: "200 GET /api/healthz/",
		},
		{
			name: "POST with query",
			info: &UvicornLogInfo{
				StatusCode: 200,
				Method:     "POST",
				Path:       "/api/search",
				Query:      "q=test",
			},
			expected: "200 POST /api/search?q=test",
		},
		{
			name: "error status",
			info: &UvicornLogInfo{
				StatusCode: 500,
				Method:     "GET",
				Path:       "/api/error",
			},
			expected: "500 GET /api/error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCleanedMessage(tt.info)
			if result != tt.expected {
				t.Errorf("BuildCleanedMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}
