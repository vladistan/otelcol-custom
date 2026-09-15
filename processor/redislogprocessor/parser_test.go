// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redislogprocessor

import (
	"testing"
)

func TestIsRedisLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "master notice log",
			input:    "1:M 31 Dec 2025 03:04:22.172 * Background saving terminated with success",
			expected: true,
		},
		{
			name:     "child notice log",
			input:    "5688:C 31 Dec 2025 03:04:22.141 * RDB: 0 MB of memory used by copy-on-write",
			expected: true,
		},
		{
			name:     "master saving log",
			input:    "1:M 31 Dec 2025 03:04:22.070 * 10000 changes in 60 seconds. Saving...",
			expected: true,
		},
		{
			name:     "warning log",
			input:    "1:M 31 Dec 2025 03:04:22.070 # Connection reset by peer",
			expected: true,
		},
		{
			name:     "sentry log - not redis",
			input:    "02:30:21 [INFO] sentry.access.api: api.access (method='GET')",
			expected: false,
		},
		{
			name:     "pgbouncer log - not redis",
			input:    "2025-12-31 02:30:17.889 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@127.0.0.1:55448",
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
			result := IsRedisLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsRedisLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseRedisLog(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectNil    bool
		expectedPID  string
		expectedRole string
		expectedLvl  string
		expectedMsg  string
	}{
		{
			name:         "master background save complete",
			input:        "1:M 31 Dec 2025 03:04:22.172 * Background saving terminated with success",
			expectNil:    false,
			expectedPID:  "1",
			expectedRole: "master",
			expectedLvl:  "notice",
			expectedMsg:  "Background saving terminated with success",
		},
		{
			name:         "child DB saved",
			input:        "5688:C 31 Dec 2025 03:04:22.140 * DB saved on disk",
			expectNil:    false,
			expectedPID:  "5688",
			expectedRole: "child",
			expectedLvl:  "notice",
			expectedMsg:  "DB saved on disk",
		},
		{
			name:         "child RDB memory",
			input:        "5688:C 31 Dec 2025 03:04:22.141 * RDB: 0 MB of memory used by copy-on-write",
			expectNil:    false,
			expectedPID:  "5688",
			expectedRole: "child",
			expectedLvl:  "notice",
			expectedMsg:  "RDB: 0 MB of memory used by copy-on-write",
		},
		{
			name:         "master saving trigger",
			input:        "1:M 31 Dec 2025 03:04:22.070 * 10000 changes in 60 seconds. Saving...",
			expectNil:    false,
			expectedPID:  "1",
			expectedRole: "master",
			expectedLvl:  "notice",
			expectedMsg:  "10000 changes in 60 seconds. Saving...",
		},
		{
			name:         "master background save started",
			input:        "1:M 31 Dec 2025 03:04:22.071 * Background saving started by pid 5688",
			expectNil:    false,
			expectedPID:  "1",
			expectedRole: "master",
			expectedLvl:  "notice",
			expectedMsg:  "Background saving started by pid 5688",
		},
		{
			name:         "warning log",
			input:        "1:M 31 Dec 2025 03:04:22.070 # Connection reset by peer",
			expectNil:    false,
			expectedPID:  "1",
			expectedRole: "master",
			expectedLvl:  "warning",
			expectedMsg:  "Connection reset by peer",
		},
		{
			name:      "not a redis log",
			input:     "Just some random text",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRedisLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseRedisLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseRedisLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.PID != tt.expectedPID {
				t.Errorf("PID = %q, want %q", result.PID, tt.expectedPID)
			}

			if result.RoleName != tt.expectedRole {
				t.Errorf("RoleName = %q, want %q", result.RoleName, tt.expectedRole)
			}

			if result.LevelName != tt.expectedLvl {
				t.Errorf("LevelName = %q, want %q", result.LevelName, tt.expectedLvl)
			}

			if result.Message != tt.expectedMsg {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMsg)
			}
		})
	}
}

func TestSeverityFromLevel(t *testing.T) {
	tests := []struct {
		levelChar    string
		expectedNum  int
		expectedText string
	}{
		{".", 5, "DEBUG"},
		{"-", 5, "DEBUG"},
		{"*", 9, "INFO"},
		{"#", 13, "WARN"},
		{"?", 9, "INFO"}, // unknown defaults to INFO
	}

	for _, tt := range tests {
		t.Run(tt.levelChar, func(t *testing.T) {
			num, text := SeverityFromLevel(tt.levelChar)
			if num != tt.expectedNum {
				t.Errorf("SeverityFromLevel(%q) num = %d, want %d", tt.levelChar, num, tt.expectedNum)
			}
			if text != tt.expectedText {
				t.Errorf("SeverityFromLevel(%q) text = %q, want %q", tt.levelChar, text, tt.expectedText)
			}
		})
	}
}

func TestBuildCleanedMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *RedisLogInfo
		expected string
	}{
		{
			name: "master message",
			info: &RedisLogInfo{
				RoleName: "master",
				Message:  "Background saving terminated with success",
			},
			expected: "[master] Background saving terminated with success",
		},
		{
			name: "child message",
			info: &RedisLogInfo{
				RoleName: "child",
				Message:  "DB saved on disk",
			},
			expected: "[child] DB saved on disk",
		},
		{
			name: "slave message",
			info: &RedisLogInfo{
				RoleName: "slave",
				Message:  "MASTER <-> REPLICA sync started",
			},
			expected: "[slave] MASTER <-> REPLICA sync started",
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
