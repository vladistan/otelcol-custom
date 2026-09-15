// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snubalogprocessor

import (
	"testing"
)

func TestIsSnubaLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "rust tracing JSON - inserted rows",
			input:    `{"timestamp":"2025-12-31T03:16:39.015154Z","level":"INFO","fields":{"message":"Inserted 1 rows"},"target":"rust_snuba::strategies::clickhouse::writer_v2"}`,
			expected: true,
		},
		{
			name:     "rust tracing JSON - consumer startup",
			input:    `{"timestamp":"2025-12-30T21:29:05.328766Z","level":"INFO","fields":{"message":"Storage: errors, ClickHouse Table Name: errors_local"},"target":"rust_snuba::consumer"}`,
			expected: true,
		},
		{
			name:     "rust tracing JSON - kafka",
			input:    `{"timestamp":"2025-12-30T21:29:08.330661Z","level":"INFO","fields":{"message":"Kafka consumer member id: \"rdkafka-ad4789e3-7a54-40ec-9db6-b146864f2cba\""},"target":"sentry_arroyo::backends::kafka"}`,
			expected: true,
		},
		{
			name:     "python logging format",
			input:    "2025-12-30 21:28:06,251 Initializing Snuba...",
			expected: true,
		},
		{
			name:     "python logging format - timing",
			input:    "2025-12-30 21:28:31,249 Snuba initialization took 25.00226184400003s",
			expected: true,
		},
		{
			name:     "redis log - not snuba",
			input:    "1:M 31 Dec 2025 03:04:22.172 * Background saving terminated with success",
			expected: false,
		},
		{
			name:     "sentry log - not snuba",
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
			result := IsSnubaLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsSnubaLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseSnubaLog(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectNil    bool
		expectedMsg  string
		expectedLvl  string
		expectedTgt  string
		expectedStor string
		expectedJSON bool
	}{
		{
			name:         "rust tracing - inserted rows",
			input:        `{"timestamp":"2025-12-31T03:16:39.015154Z","level":"INFO","fields":{"message":"Inserted 1 rows"},"target":"rust_snuba::strategies::clickhouse::writer_v2"}`,
			expectNil:    false,
			expectedMsg:  "Inserted 1 rows",
			expectedLvl:  "INFO",
			expectedTgt:  "rust_snuba::strategies::clickhouse::writer_v2",
			expectedStor: "",
			expectedJSON: true,
		},
		{
			name:         "rust tracing - with storage",
			input:        `{"timestamp":"2025-12-30T21:29:07.044337Z","level":"INFO","fields":{"message":"Starting consumer for \"transactions\"","storage":"transactions"},"target":"rust_snuba::consumer"}`,
			expectNil:    false,
			expectedMsg:  `Starting consumer for "transactions"`,
			expectedLvl:  "INFO",
			expectedTgt:  "rust_snuba::consumer",
			expectedStor: "transactions",
			expectedJSON: true,
		},
		{
			name:         "rust tracing - partitions assigned",
			input:        `{"timestamp":"2025-12-30T21:29:08.332048Z","level":"INFO","fields":{"message":"New partitions assigned: {Partition { topic: Topic(\"events\"), index: 0 }: 731071}"},"target":"sentry_arroyo::processing"}`,
			expectNil:    false,
			expectedMsg:  `New partitions assigned: {Partition { topic: Topic("events"), index: 0 }: 731071}`,
			expectedLvl:  "INFO",
			expectedTgt:  "sentry_arroyo::processing",
			expectedStor: "",
			expectedJSON: true,
		},
		{
			name:         "python logging - init",
			input:        "2025-12-30 21:28:06,251 Initializing Snuba...",
			expectNil:    false,
			expectedMsg:  "Initializing Snuba...",
			expectedLvl:  "INFO",
			expectedTgt:  "",
			expectedStor: "",
			expectedJSON: false,
		},
		{
			name:         "python logging - timing",
			input:        "2025-12-30 21:28:31,249 Snuba initialization took 25.00226184400003s",
			expectNil:    false,
			expectedMsg:  "Snuba initialization took 25.00226184400003s",
			expectedLvl:  "INFO",
			expectedTgt:  "",
			expectedStor: "",
			expectedJSON: false,
		},
		{
			name:      "not a snuba log",
			input:     "Just some random text",
			expectNil: true,
		},
		{
			name:      "invalid JSON",
			input:     `{"timestamp":"2025-12-31","broken`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSnubaLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseSnubaLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseSnubaLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Message != tt.expectedMsg {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMsg)
			}

			if result.Level != tt.expectedLvl {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLvl)
			}

			if result.Target != tt.expectedTgt {
				t.Errorf("Target = %q, want %q", result.Target, tt.expectedTgt)
			}

			if result.Storage != tt.expectedStor {
				t.Errorf("Storage = %q, want %q", result.Storage, tt.expectedStor)
			}

			if result.IsJSON != tt.expectedJSON {
				t.Errorf("IsJSON = %v, want %v", result.IsJSON, tt.expectedJSON)
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
		{"TRACE", 1, "TRACE"},
		{"DEBUG", 5, "DEBUG"},
		{"INFO", 9, "INFO"},
		{"WARN", 13, "WARN"},
		{"WARNING", 13, "WARN"},
		{"ERROR", 17, "ERROR"},
		{"FATAL", 21, "FATAL"},
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

func TestBuildCleanedMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *SnubaLogInfo
		expected string
	}{
		{
			name: "rust tracing with target",
			info: &SnubaLogInfo{
				Message: "Inserted 1 rows",
				Target:  "rust_snuba::strategies::clickhouse::writer_v2",
				IsJSON:  true,
			},
			expected: "[writer_v2] Inserted 1 rows",
		},
		{
			name: "rust tracing - consumer",
			info: &SnubaLogInfo{
				Message: "Starting consumer for \"transactions\"",
				Target:  "rust_snuba::consumer",
				IsJSON:  true,
			},
			expected: "[consumer] Starting consumer for \"transactions\"",
		},
		{
			name: "rust tracing - kafka",
			info: &SnubaLogInfo{
				Message: "Kafka consumer member id: \"abc\"",
				Target:  "sentry_arroyo::backends::kafka",
				IsJSON:  true,
			},
			expected: "[kafka] Kafka consumer member id: \"abc\"",
		},
		{
			name: "python logging - no target",
			info: &SnubaLogInfo{
				Message: "Initializing Snuba...",
				IsJSON:  false,
			},
			expected: "Initializing Snuba...",
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

func TestContainsComponent(t *testing.T) {
	tests := []struct {
		containerName string
		component     string
		expected      bool
	}{
		{"sentry-self-hosted-snuba-errors-consumer-1", "snuba", true},
		{"sentry-self-hosted-snuba-api-1", "snuba", true},
		{"snuba-api", "snuba", true},
		{"sentry-web-1", "snuba", false},
		{"redis-1", "snuba", false},
		{"snuba", "snuba", true},
	}

	for _, tt := range tests {
		t.Run(tt.containerName, func(t *testing.T) {
			result := containsComponent(tt.containerName, tt.component)
			if result != tt.expected {
				t.Errorf("containsComponent(%q, %q) = %v, want %v", tt.containerName, tt.component, result, tt.expected)
			}
		})
	}
}
