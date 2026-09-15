// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

import (
	"testing"
)

func TestParseNginxLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		expected *NginxLogInfo
	}{
		{
			name:  "combined format with x-forwarded-for",
			input: `192.0.2.1 - - [31/Dec/2025:00:30:00 +0000] "POST /api/2/envelope/ HTTP/1.1" 200 41 "-" "sentry.go/0.40.0" "192.0.2.1:46331"`,
			expected: &NginxLogInfo{
				ClientAddr:   "192.0.2.1",
				Method:       "POST",
				Path:         "/api/2/envelope/",
				Protocol:     "HTTP/1.1",
				StatusCode:   200,
				BodyBytes:    41,
				UserAgent:    "sentry.go/0.40.0",
				ForwardedFor: "192.0.2.1:46331",
				CleanMessage: "POST /api/2/envelope/ 200",
			},
		},
		{
			name:  "combined format without x-forwarded-for",
			input: `127.0.0.1 - - [31/Dec/2025:00:30:03 +0000] "GET / HTTP/1.1" 302 0 "-" "curl/8.14.1" "-"`,
			expected: &NginxLogInfo{
				ClientAddr:   "127.0.0.1",
				Method:       "GET",
				Path:         "/",
				Protocol:     "HTTP/1.1",
				StatusCode:   302,
				BodyBytes:    0,
				UserAgent:    "curl/8.14.1",
				CleanMessage: "GET / 302",
			},
		},
		{
			name:  "combined format with query string and referer",
			input: `192.0.2.1 - - [31/Dec/2025:00:01:40 +0000] "GET /api/0/organizations/sentry/issues/?collapse=stats&limit=25 HTTP/1.1" 200 2 "https://sentry.r4.example.com/issues/" "Mozilla/5.0 (Macintosh)" "192.0.2.1:51244"`,
			expected: &NginxLogInfo{
				ClientAddr:   "192.0.2.1",
				Method:       "GET",
				Path:         "/api/0/organizations/sentry/issues/",
				Query:        "collapse=stats&limit=25",
				Protocol:     "HTTP/1.1",
				StatusCode:   200,
				BodyBytes:    2,
				Referer:      "https://sentry.r4.example.com/issues/",
				UserAgent:    "Mozilla/5.0 (Macintosh)",
				ForwardedFor: "192.0.2.1:51244",
				CleanMessage: "GET /api/0/organizations/sentry/issues/?collapse=stats&limit=25 200",
			},
		},
		{
			name:  "health check with elb",
			input: `172.31.1.150 - - [31/Dec/2025:00:29:05 +0000] "GET / HTTP/1.1" 302 0 "-" "ELB-HealthChecker/2.0" "-"`,
			expected: &NginxLogInfo{
				ClientAddr:   "172.31.1.150",
				Method:       "GET",
				Path:         "/",
				Protocol:     "HTTP/1.1",
				StatusCode:   302,
				BodyBytes:    0,
				UserAgent:    "ELB-HealthChecker/2.0",
				CleanMessage: "GET / 302",
			},
		},
		{
			name:  "json wrapped format",
			input: `{"text":"192.0.2.1 - - [31/Dec/2025:00:30:00 +0000] \"POST /api/2/envelope/ HTTP/1.1\" 200 41 \"-\" \"sentry.go/0.40.0\" \"192.0.2.1:46331\""}`,
			expected: &NginxLogInfo{
				ClientAddr:   "192.0.2.1",
				Method:       "POST",
				Path:         "/api/2/envelope/",
				Protocol:     "HTTP/1.1",
				StatusCode:   200,
				BodyBytes:    41,
				UserAgent:    "sentry.go/0.40.0",
				ForwardedFor: "192.0.2.1:46331",
				CleanMessage: "POST /api/2/envelope/ 200",
			},
		},
		{
			name:    "not nginx log",
			input:   `{"level":"info","ts":1735603800,"msg":"Starting server"}`,
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseNginxLog(tt.input)

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected result, got nil")
			}

			if result.ClientAddr != tt.expected.ClientAddr {
				t.Errorf("ClientAddr: got %q, want %q", result.ClientAddr, tt.expected.ClientAddr)
			}
			if result.Method != tt.expected.Method {
				t.Errorf("Method: got %q, want %q", result.Method, tt.expected.Method)
			}
			if result.Path != tt.expected.Path {
				t.Errorf("Path: got %q, want %q", result.Path, tt.expected.Path)
			}
			if result.Query != tt.expected.Query {
				t.Errorf("Query: got %q, want %q", result.Query, tt.expected.Query)
			}
			if result.Protocol != tt.expected.Protocol {
				t.Errorf("Protocol: got %q, want %q", result.Protocol, tt.expected.Protocol)
			}
			if result.StatusCode != tt.expected.StatusCode {
				t.Errorf("StatusCode: got %d, want %d", result.StatusCode, tt.expected.StatusCode)
			}
			if result.BodyBytes != tt.expected.BodyBytes {
				t.Errorf("BodyBytes: got %d, want %d", result.BodyBytes, tt.expected.BodyBytes)
			}
			if result.Referer != tt.expected.Referer {
				t.Errorf("Referer: got %q, want %q", result.Referer, tt.expected.Referer)
			}
			if result.UserAgent != tt.expected.UserAgent {
				t.Errorf("UserAgent: got %q, want %q", result.UserAgent, tt.expected.UserAgent)
			}
			if result.ForwardedFor != tt.expected.ForwardedFor {
				t.Errorf("ForwardedFor: got %q, want %q", result.ForwardedFor, tt.expected.ForwardedFor)
			}
			if result.CleanMessage != tt.expected.CleanMessage {
				t.Errorf("CleanMessage: got %q, want %q", result.CleanMessage, tt.expected.CleanMessage)
			}
		})
	}
}

func TestIsNginxLog(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`192.0.2.1 - - [31/Dec/2025:00:30:00 +0000] "POST /api HTTP/1.1" 200 41`, true},
		{`127.0.0.1 - user [31/Dec/2025:00:30:03 +0000] "GET / HTTP/1.1" 302 0`, true},
		{`{"level":"info","ts":1735603800,"msg":"Starting server"}`, false},
		{`Dec 31 00:30:00 host sshd[1234]: Connection from 192.168.1.1`, false},
		{``, false},
	}

	for _, tt := range tests {
		t.Run(tt.input[:min(30, len(tt.input))], func(t *testing.T) {
			result := IsNginxLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsNginxLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
