// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kafkalogprocessor

import (
	"testing"
)

func TestIsKafkaLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "standard info log",
			input:    "[2025-12-31 01:15:07,291] INFO Cleaner 0: Beginning cleaning of log snuba-commit-log-0 (kafka.log.LogCleaner)",
			expected: true,
		},
		{
			name:     "warn log",
			input:    "[2025-12-31 02:30:00,123] WARN Some warning message (kafka.server.ReplicaManager)",
			expected: true,
		},
		{
			name:     "error log",
			input:    "[2025-12-31 03:45:12,456] ERROR Connection failed (kafka.network.Processor)",
			expected: true,
		},
		{
			name:     "debug log",
			input:    "[2025-12-31 04:00:00,000] DEBUG Detailed message (kafka.log.Log)",
			expected: true,
		},
		{
			name:     "multi-line header",
			input:    "[2025-12-31 03:30:08,003] INFO [kafka-log-cleaner-thread-0]: ",
			expected: true,
		},
		{
			name:     "cleaner stats log",
			input:    "[2025-12-31 01:15:07,327] INFO [kafka-log-cleaner-thread-0]: \n\tLog cleaner thread 0 cleaned log snuba-commit-log-0",
			expected: true,
		},
		{
			name:     "snuba log - not kafka",
			input:    `{"timestamp":"2025-12-31T03:16:39.015154Z","level":"INFO","fields":{"message":"Inserted 1 rows"},"target":"rust_snuba"}`,
			expected: false,
		},
		{
			name:     "sentry log - not kafka",
			input:    "02:30:21 [INFO] sentry.access.api: api.access (method='GET')",
			expected: false,
		},
		{
			name:     "postgres log - not kafka",
			input:    "2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key",
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
			result := IsKafkaLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsKafkaLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseKafkaLog(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectNil     bool
		expectedLevel string
		expectedMsg   string
		expectedLog   string
		expectedShort string
	}{
		{
			name:          "cleaner beginning log",
			input:         "[2025-12-31 01:15:07,291] INFO Cleaner 0: Beginning cleaning of log snuba-commit-log-0 (kafka.log.LogCleaner)",
			expectNil:     false,
			expectedLevel: "INFO",
			expectedMsg:   "Cleaner 0: Beginning cleaning of log snuba-commit-log-0",
			expectedLog:   "kafka.log.LogCleaner",
			expectedShort: "LogCleaner",
		},
		{
			name:          "cleaner building offset map",
			input:         "[2025-12-31 01:15:07,568] INFO Cleaner 0: Building offset map for snuba-commit-log-0... (kafka.log.LogCleaner)",
			expectNil:     false,
			expectedLevel: "INFO",
			expectedMsg:   "Cleaner 0: Building offset map for snuba-commit-log-0...",
			expectedLog:   "kafka.log.LogCleaner",
			expectedShort: "LogCleaner",
		},
		{
			name:          "cleaner swapping segment",
			input:         "[2025-12-31 01:15:07,323] INFO Cleaner 0: Swapping in cleaned segment LogSegment(baseOffset=130, size=181) for segment(s) List() in log Log(dir=/var/lib/kafka/data/snuba-commit-log-0) (kafka.log.LogCleaner)",
			expectNil:     false,
			expectedLevel: "INFO",
			expectedMsg:   "Cleaner 0: Swapping in cleaned segment LogSegment(baseOffset=130, size=181) for segment(s) List() in log Log(dir=/var/lib/kafka/data/snuba-commit-log-0)",
			expectedLog:   "kafka.log.LogCleaner",
			expectedShort: "LogCleaner",
		},
		{
			name:          "multi-line header",
			input:         "[2025-12-31 03:30:08,003] INFO [kafka-log-cleaner-thread-0]: ",
			expectNil:     false,
			expectedLevel: "INFO",
			expectedMsg:   "[kafka-log-cleaner-thread-0]",
			expectedLog:   "kafka-log-cleaner-thread-0",
			expectedShort: "log-cleaner-thread-0",
		},
		{
			name:          "warn level",
			input:         "[2025-12-31 02:30:00,123] WARN Replica fetch lag too high (kafka.server.ReplicaManager)",
			expectNil:     false,
			expectedLevel: "WARN",
			expectedMsg:   "Replica fetch lag too high",
			expectedLog:   "kafka.server.ReplicaManager",
			expectedShort: "ReplicaManager",
		},
		{
			name:          "error level",
			input:         "[2025-12-31 03:45:12,456] ERROR Connection to broker lost (kafka.network.Processor)",
			expectNil:     false,
			expectedLevel: "ERROR",
			expectedMsg:   "Connection to broker lost",
			expectedLog:   "kafka.network.Processor",
			expectedShort: "Processor",
		},
		{
			name:          "debug level",
			input:         "[2025-12-31 04:00:00,000] DEBUG Processing request from client (kafka.server.KafkaApis)",
			expectNil:     false,
			expectedLevel: "DEBUG",
			expectedMsg:   "Processing request from client",
			expectedLog:   "kafka.server.KafkaApis",
			expectedShort: "KafkaApis",
		},
		{
			name:      "not a kafka log",
			input:     "Just some random text",
			expectNil: true,
		},
		{
			name:      "postgres log",
			input:     "2025-12-31 00:01:39.618 UTC [1975] ERROR:  duplicate key",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseKafkaLog(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseKafkaLog(%q) expected nil, got %+v", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseKafkaLog(%q) returned nil, expected non-nil", tt.input)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}

			if result.Message != tt.expectedMsg {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMsg)
			}

			if result.Logger != tt.expectedLog {
				t.Errorf("Logger = %q, want %q", result.Logger, tt.expectedLog)
			}

			if result.ShortName != tt.expectedShort {
				t.Errorf("ShortName = %q, want %q", result.ShortName, tt.expectedShort)
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
		{"ERROR", 17, "ERROR"},
		{"FATAL", 21, "FATAL"},
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
		info     *KafkaLogInfo
		expected string
	}{
		{
			name: "with logger",
			info: &KafkaLogInfo{
				Message:   "Cleaner 0: Beginning cleaning of log",
				ShortName: "LogCleaner",
			},
			expected: "[LogCleaner] Cleaner 0: Beginning cleaning of log",
		},
		{
			name: "thread name logger",
			info: &KafkaLogInfo{
				Message:   "[kafka-log-cleaner-thread-0]",
				ShortName: "log-cleaner-thread-0",
			},
			expected: "[log-cleaner-thread-0] [kafka-log-cleaner-thread-0]",
		},
		{
			name: "no logger",
			info: &KafkaLogInfo{
				Message:   "Some message without logger",
				ShortName: "",
			},
			expected: "Some message without logger",
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

func TestExtractShortLoggerName(t *testing.T) {
	tests := []struct {
		logger   string
		expected string
	}{
		{"kafka.log.LogCleaner", "LogCleaner"},
		{"kafka.server.ReplicaManager", "ReplicaManager"},
		{"kafka.network.Processor", "Processor"},
		{"kafka-log-cleaner-thread-0", "log-cleaner-thread-0"},
		{"SimpleLogger", "SimpleLogger"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.logger, func(t *testing.T) {
			result := extractShortLoggerName(tt.logger)
			if result != tt.expected {
				t.Errorf("extractShortLoggerName(%q) = %q, want %q", tt.logger, result, tt.expected)
			}
		})
	}
}
