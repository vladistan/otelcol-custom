// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kafkalogprocessor

import (
	"regexp"
	"strings"
)

// KafkaLogInfo holds parsed information from a Kafka log entry.
type KafkaLogInfo struct {
	Level     string // INFO, WARN, ERROR, DEBUG, TRACE
	Message   string // The log message
	Logger    string // The logger class (e.g., kafka.log.LogCleaner)
	ShortName string // Short logger name (e.g., LogCleaner)
}

// Kafka log format: [2025-12-31 01:15:07,291] INFO message (kafka.log.LogCleaner)
// Also handles: [2025-12-31 01:15:07,291] INFO [thread-name] message (kafka.log.LogCleaner)
var kafkaLogPattern = regexp.MustCompile(
	`^\[(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2},\d{3})\]\s+` + // [timestamp]
		`(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+` + // level
		`(.+?)\s*` + // message (non-greedy)
		`\(([^)]+)\)\s*$`, // (logger)
)

// Multi-line Kafka log (like cleaner stats) - just the header
var kafkaMultiLinePattern = regexp.MustCompile(
	`^\[(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2},\d{3})\]\s+` + // [timestamp]
		`(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+` + // level
		`\[([^\]]+)\]:\s*$`, // [thread-name]:
)

// IsKafkaLog checks if a log line looks like a Kafka log entry.
func IsKafkaLog(body string) bool {
	if len(body) < 25 {
		return false
	}

	// Must start with [timestamp]
	if !strings.HasPrefix(body, "[") {
		return false
	}

	// Quick check for timestamp format
	if len(body) > 24 && body[5] == '-' && body[8] == '-' && body[11] == ' ' {
		return true
	}

	return false
}

// ParseKafkaLog parses a Kafka log entry and extracts structured information.
func ParseKafkaLog(body string) *KafkaLogInfo {
	// Try standard format first
	if matches := kafkaLogPattern.FindStringSubmatch(body); len(matches) == 5 {
		logger := matches[4]
		shortName := extractShortLoggerName(logger)

		return &KafkaLogInfo{
			Level:     matches[2],
			Message:   strings.TrimSpace(matches[3]),
			Logger:    logger,
			ShortName: shortName,
		}
	}

	// Try multi-line header format (cleaner stats, etc.)
	if matches := kafkaMultiLinePattern.FindStringSubmatch(body); len(matches) == 4 {
		return &KafkaLogInfo{
			Level:     matches[2],
			Message:   "[" + matches[3] + "]",
			Logger:    matches[3],
			ShortName: extractShortLoggerName(matches[3]),
		}
	}

	// Fallback: try to extract just level and message without logger
	fallbackPattern := regexp.MustCompile(
		`^\[(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2},\d{3})\]\s+` +
			`(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+` +
			`(.+)$`,
	)
	if matches := fallbackPattern.FindStringSubmatch(body); len(matches) == 4 {
		return &KafkaLogInfo{
			Level:   matches[2],
			Message: strings.TrimSpace(matches[3]),
		}
	}

	return nil
}

// extractShortLoggerName extracts the short class name from a fully qualified logger.
// e.g., "kafka.log.LogCleaner" -> "LogCleaner"
// e.g., "kafka-log-cleaner-thread-0" -> "log-cleaner-thread-0"
func extractShortLoggerName(logger string) string {
	// Handle dot-separated class names
	if idx := strings.LastIndex(logger, "."); idx != -1 {
		return logger[idx+1:]
	}
	// Handle kafka- prefixed thread names
	if strings.HasPrefix(logger, "kafka-") {
		return logger[6:]
	}
	return logger
}

// SeverityFromLevel converts a Kafka log level to OTEL severity.
// Returns severity number and text.
func SeverityFromLevel(level string) (int, string) {
	switch strings.ToUpper(level) {
	case "TRACE":
		return 1, "TRACE"
	case "DEBUG":
		return 5, "DEBUG"
	case "INFO":
		return 9, "INFO"
	case "WARN":
		return 13, "WARN"
	case "ERROR":
		return 17, "ERROR"
	case "FATAL":
		return 21, "FATAL"
	default:
		return 9, "INFO"
	}
}

// BuildCleanedMessage creates a human-readable message from parsed fields.
// Format: [{short_logger}] {message}
func BuildCleanedMessage(info *KafkaLogInfo) string {
	if info.ShortName == "" {
		return info.Message
	}

	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(info.ShortName)
	sb.WriteString("] ")
	sb.WriteString(info.Message)
	return sb.String()
}
