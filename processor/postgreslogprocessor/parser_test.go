// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package postgreslogprocessor

import (
	"testing"
)

func TestIsPostgresLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "error log",
			input:    "2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key value violates unique constraint",
			expected: true,
		},
		{
			name:     "log level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] LOG:  checkpoint starting: time",
			expected: true,
		},
		{
			name:     "warning level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] WARNING:  some warning message",
			expected: true,
		},
		{
			name:     "notice level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] NOTICE:  some notice",
			expected: true,
		},
		{
			name:     "info level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] INFO:  some info",
			expected: true,
		},
		{
			name:     "debug level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] DEBUG1:  debug message",
			expected: true,
		},
		{
			name:     "fatal level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] FATAL:  fatal error",
			expected: true,
		},
		{
			name:     "panic level",
			input:    "2025-12-31 00:01:39.618 UTC [1975] PANIC:  panic message",
			expected: true,
		},
		{
			name:     "detail line",
			input:    "2025-12-31 00:01:39.618 UTC [1975] DETAIL:  Key (group_id, user_id)=(45, 2) already exists.",
			expected: true,
		},
		{
			name:     "statement line",
			input:    "2025-12-31 00:01:39.618 UTC [1975] STATEMENT:  INSERT INTO table VALUES (1, 2)",
			expected: true,
		},
		{
			name:     "kafka log - not postgres",
			input:    "[2025-12-31 01:15:07,291] INFO Cleaner 0: Cleaning log (kafka.log.LogCleaner)",
			expected: false,
		},
		{
			name:     "snuba log - not postgres",
			input:    `{"timestamp":"2025-12-31T03:16:39.015154Z","level":"INFO","fields":{"message":"Inserted 1 rows"}}`,
			expected: false,
		},
		{
			name:     "sentry log - not postgres",
			input:    "02:30:21 [INFO] sentry.access.api: api.access (method='GET')",
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
			result := IsPostgresLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsPostgresLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParsePostgresLog(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectNil     bool
		expectedLevel string
		expectedPID   string
		expectedMsg   string
		expectedDet   string
		expectedStmt  string
	}{
		{
			name:          "simple error",
			input:         "2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key value violates unique constraint \"sentry_groupsubscription_group_id_user_id_62180a04_uniq\"",
			expectNil:     false,
			expectedLevel: "ERROR",
			expectedPID:   "1975",
			expectedMsg:   `duplicate key value violates unique constraint "sentry_groupsubscription_group_id_user_id_62180a04_uniq"`,
		},
		{
			name: "error with detail and statement",
			input: `2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key value violates unique constraint "sentry_groupsubscription_group_id_user_id_62180a04_uniq"
2025-12-31 00:01:39.618 UTC [1975] DETAIL:  Key (group_id, user_id)=(45, 2) already exists.
2025-12-31 00:01:39.618 UTC [1975] STATEMENT:  INSERT INTO "sentry_groupsubscription" ("project_id") VALUES (2)`,
			expectNil:     false,
			expectedLevel: "ERROR",
			expectedPID:   "1975",
			expectedMsg:   `duplicate key value violates unique constraint "sentry_groupsubscription_group_id_user_id_62180a04_uniq"`,
			expectedDet:   "Key (group_id, user_id)=(45, 2) already exists.",
			expectedStmt:  `INSERT INTO "sentry_groupsubscription" ("project_id") VALUES (2)`,
		},
		{
			name:          "log level - checkpoint",
			input:         "2025-12-31 00:01:39.618 UTC [1] LOG:  checkpoint starting: time",
			expectNil:     false,
			expectedLevel: "LOG",
			expectedPID:   "1",
			expectedMsg:   "checkpoint starting: time",
		},
		{
			name:          "warning level",
			input:         "2025-12-31 00:01:39.618 UTC [123] WARNING:  could not open statistics file",
			expectNil:     false,
			expectedLevel: "WARNING",
			expectedPID:   "123",
			expectedMsg:   "could not open statistics file",
		},
		{
			name:          "debug level",
			input:         "2025-12-31 00:01:39.618 UTC [456] DEBUG1:  autovacuum: processing database",
			expectNil:     false,
			expectedLevel: "DEBUG",
			expectedPID:   "456",
			expectedMsg:   "autovacuum: processing database",
		},
		{
			name:          "fatal level",
			input:         "2025-12-31 00:01:39.618 UTC [789] FATAL:  database system is shutting down",
			expectNil:     false,
			expectedLevel: "FATAL",
			expectedPID:   "789",
			expectedMsg:   "database system is shutting down",
		},
		{
			name:          "notice level",
			input:         "2025-12-31 00:01:39.618 UTC [111] NOTICE:  table \"test\" does not exist, skipping",
			expectNil:     false,
			expectedLevel: "NOTICE",
			expectedPID:   "111",
			expectedMsg:   `table "test" does not exist, skipping`,
		},
		{
			name:      "standalone detail line - should return nil",
			input:     "2025-12-31 00:01:39.618 UTC [1975] DETAIL:  Key (group_id, user_id)=(45, 2) already exists.",
			expectNil: true,
		},
		{
			name:      "standalone statement line - should return nil",
			input:     "2025-12-31 00:01:39.618 UTC [1975] STATEMENT:  INSERT INTO table VALUES (1, 2)",
			expectNil: true,
		},
		{
			name:      "not a postgres log",
			input:     "Just some random text",
			expectNil: true,
		},
		{
			name:      "kafka log",
			input:     "[2025-12-31 01:15:07,291] INFO Cleaner 0: Cleaning log (kafka.log.LogCleaner)",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePostgresLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParsePostgresLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParsePostgresLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.PID != tt.expectedPID {
				t.Errorf("PID = %q, want %q", result.PID, tt.expectedPID)
			}

			if result.Message != tt.expectedMsg {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMsg)
			}

			if tt.expectedDet != "" && result.Detail != tt.expectedDet {
				t.Errorf("Detail = %q, want %q", result.Detail, tt.expectedDet)
			}

			if tt.expectedStmt != "" && result.Statement != tt.expectedStmt {
				t.Errorf("Statement = %q, want %q", result.Statement, tt.expectedStmt)
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
		{"DEBUG1", 5, "DEBUG"},
		{"DEBUG2", 5, "DEBUG"},
		{"DEBUG3", 5, "DEBUG"},
		{"INFO", 9, "INFO"},
		{"NOTICE", 9, "INFO"},
		{"LOG", 9, "INFO"},
		{"WARNING", 13, "WARN"},
		{"ERROR", 17, "ERROR"},
		{"FATAL", 21, "FATAL"},
		{"PANIC", 21, "FATAL"},
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

func TestBuildCleanedMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *PostgresLogInfo
		expected string
	}{
		{
			name: "simple error",
			info: &PostgresLogInfo{
				Level:   "ERROR",
				Message: "connection refused",
			},
			expected: "[ERROR] connection refused",
		},
		{
			name: "constraint violation with detail",
			info: &PostgresLogInfo{
				Level:   "ERROR",
				Message: `duplicate key value violates unique constraint "test_pkey"`,
				Detail:  "Key (id)=(1) already exists.",
			},
			expected: `[ERROR] duplicate key value violates unique constraint "test_pkey" (Key (id)=(1) already exists.)`,
		},
		{
			name: "log level",
			info: &PostgresLogInfo{
				Level:   "LOG",
				Message: "checkpoint starting: time",
			},
			expected: "[LOG] checkpoint starting: time",
		},
		{
			name: "warning without constraint keyword",
			info: &PostgresLogInfo{
				Level:   "WARNING",
				Message: "could not open statistics file",
				Detail:  "Some detail that won't be appended",
			},
			expected: "[WARNING] could not open statistics file",
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

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		level    string
		expected string
	}{
		{"DEBUG1", "DEBUG"},
		{"DEBUG2", "DEBUG"},
		{"DEBUG3", "DEBUG"},
		{"DEBUG4", "DEBUG"},
		{"DEBUG5", "DEBUG"},
		{"DEBUG", "DEBUG"},
		{"ERROR", "ERROR"},
		{"LOG", "LOG"},
		{"WARNING", "WARNING"},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			result := normalizeLevel(tt.level)
			if result != tt.expected {
				t.Errorf("normalizeLevel(%q) = %q, want %q", tt.level, result, tt.expected)
			}
		})
	}
}
