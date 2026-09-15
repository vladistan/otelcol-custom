// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pgbouncerlogprocessor

import (
	"testing"
	"time"
)

func TestIsPgBouncerLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "login attempt",
			input:    "2025-12-31 01:46:17.407 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 login attempt: db=postgres user=postgres tls=no replication=no",
			expected: true,
		},
		{
			name:     "closing connection",
			input:    "2025-12-31 01:46:17.429 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 closing because: client close request (age=0s)",
			expected: true,
		},
		{
			name:     "stats line",
			input:    "2025-12-31 01:45:00.000 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us",
			expected: true,
		},
		{
			name:     "warning",
			input:    "2025-12-31 01:45:00.000 UTC [1] WARNING pooler error: no such database: test",
			expected: true,
		},
		{
			name:     "error",
			input:    "2025-12-31 01:45:00.000 UTC [1] ERROR cannot connect to server",
			expected: true,
		},
		{
			name:     "not pgbouncer log",
			input:    "192.168.1.1 - - [31/Dec/2025:01:46:17 +0000] \"GET / HTTP/1.1\" 200 612",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "short string",
			input:    "2025-12-31",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPgBouncerLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsPgBouncerLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParsePgBouncerLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *PgBouncerLogInfo
	}{
		{
			name:  "login attempt with connection info",
			input: "2025-12-31 01:46:17.407 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 login attempt: db=postgres user=postgres tls=no replication=no",
			expected: &PgBouncerLogInfo{
				Timestamp:    time.Date(2025, 12, 31, 1, 46, 17, 407000000, time.UTC),
				PID:          1,
				Level:        "LOG",
				ConnectionID: "C-0x7f828e6b34c0",
				Database:     "postgres",
				User:         "postgres",
				ClientIP:     "172.18.0.60",
				ClientPort:   39630,
				Message:      "login attempt: db=postgres user=postgres tls=no replication=no",
			},
		},
		{
			name:  "closing connection",
			input: "2025-12-31 01:46:17.429 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 closing because: client close request (age=0s)",
			expected: &PgBouncerLogInfo{
				Timestamp:    time.Date(2025, 12, 31, 1, 46, 17, 429000000, time.UTC),
				PID:          1,
				Level:        "LOG",
				ConnectionID: "C-0x7f828e6b34c0",
				Database:     "postgres",
				User:         "postgres",
				ClientIP:     "172.18.0.60",
				ClientPort:   39630,
				Message:      "closing because: client close request (age=0s)",
			},
		},
		{
			name:  "stats line without connection",
			input: "2025-12-31 01:45:00.000 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us",
			expected: &PgBouncerLogInfo{
				Timestamp:    time.Date(2025, 12, 31, 1, 45, 0, 0, time.UTC),
				PID:          1,
				Level:        "LOG",
				ConnectionID: "",
				Database:     "",
				User:         "",
				ClientIP:     "",
				ClientPort:   0,
				Message:      "stats: 0 xacts/s, 0 queries/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us",
			},
		},
		{
			name:  "warning message",
			input: "2025-12-31 01:45:00.000 UTC [1] WARNING pooler error: no such database: test",
			expected: &PgBouncerLogInfo{
				Timestamp: time.Date(2025, 12, 31, 1, 45, 0, 0, time.UTC),
				PID:       1,
				Level:     "WARNING",
				Message:   "pooler error: no such database: test",
			},
		},
		{
			name:  "server connection",
			input: "2025-12-31 01:46:17.407 UTC [1] LOG S-0x7f828e6b34c0: postgres/postgres@192.168.1.100:5432 new connection to server",
			expected: &PgBouncerLogInfo{
				Timestamp:    time.Date(2025, 12, 31, 1, 46, 17, 407000000, time.UTC),
				PID:          1,
				Level:        "LOG",
				ConnectionID: "S-0x7f828e6b34c0",
				Database:     "postgres",
				User:         "postgres",
				ClientIP:     "192.168.1.100",
				ClientPort:   5432,
				Message:      "new connection to server",
			},
		},
		{
			name:     "invalid log",
			input:    "not a pgbouncer log",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePgBouncerLog(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("ParsePgBouncerLog(%q) = %+v, want nil", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParsePgBouncerLog(%q) = nil, want %+v", tt.input, tt.expected)
			}

			// Compare fields
			if !result.Timestamp.Equal(tt.expected.Timestamp) {
				t.Errorf("Timestamp = %v, want %v", result.Timestamp, tt.expected.Timestamp)
			}
			if result.PID != tt.expected.PID {
				t.Errorf("PID = %d, want %d", result.PID, tt.expected.PID)
			}
			if result.Level != tt.expected.Level {
				t.Errorf("Level = %q, want %q", result.Level, tt.expected.Level)
			}
			if result.ConnectionID != tt.expected.ConnectionID {
				t.Errorf("ConnectionID = %q, want %q", result.ConnectionID, tt.expected.ConnectionID)
			}
			if result.Database != tt.expected.Database {
				t.Errorf("Database = %q, want %q", result.Database, tt.expected.Database)
			}
			if result.User != tt.expected.User {
				t.Errorf("User = %q, want %q", result.User, tt.expected.User)
			}
			if result.ClientIP != tt.expected.ClientIP {
				t.Errorf("ClientIP = %q, want %q", result.ClientIP, tt.expected.ClientIP)
			}
			if result.ClientPort != tt.expected.ClientPort {
				t.Errorf("ClientPort = %d, want %d", result.ClientPort, tt.expected.ClientPort)
			}
			if result.Message != tt.expected.Message {
				t.Errorf("Message = %q, want %q", result.Message, tt.expected.Message)
			}
		})
	}
}

func TestSeverityFromLevel(t *testing.T) {
	tests := []struct {
		level      string
		wantNumber int
		wantText   string
	}{
		{"DEBUG", 5, "DEBUG"},
		{"LOG", 9, "INFO"},
		{"INFO", 9, "INFO"},
		{"NOTICE", 9, "INFO"},
		{"WARNING", 13, "WARN"},
		{"ERROR", 17, "ERROR"},
		{"FATAL", 21, "FATAL"},
		{"PANIC", 23, "FATAL"},
		{"unknown", 9, "INFO"},
		{"log", 9, "INFO"},      // lowercase
		{"Warning", 13, "WARN"}, // mixed case
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			gotNumber, gotText := SeverityFromLevel(tt.level)
			if gotNumber != tt.wantNumber {
				t.Errorf("SeverityFromLevel(%q) number = %d, want %d", tt.level, gotNumber, tt.wantNumber)
			}
			if gotText != tt.wantText {
				t.Errorf("SeverityFromLevel(%q) text = %q, want %q", tt.level, gotText, tt.wantText)
			}
		})
	}
}
