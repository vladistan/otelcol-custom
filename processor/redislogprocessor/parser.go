// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redislogprocessor

import (
	"regexp"
	"strings"
)

// RedisLogInfo contains parsed information from a Redis log line.
type RedisLogInfo struct {
	// PID is the Redis process ID
	PID string

	// Role is the Redis role: M=Master, S=Slave, C=Child (RDB/AOF), X=Sentinel
	Role string

	// RoleName is the human-readable role name
	RoleName string

	// Level is the log level character: . = debug, - = verbose, * = notice, # = warning
	LevelChar string

	// LevelName is the human-readable level name
	LevelName string

	// Message is the cleaned log message (without timestamp prefix)
	Message string
}

// Redis log format: {pid}:{role} {day} {month} {year} {time} {level_char} {message}
// Example: 1:M 31 Dec 2025 03:04:22.172 * Background saving terminated with success
// Example: 5688:C 31 Dec 2025 03:04:22.141 * RDB: 0 MB of memory used by copy-on-write
var redisLogPattern = regexp.MustCompile(
	`^(\d+):([MSCX])\s+` + // pid:role
		`\d{1,2}\s+[A-Za-z]{3}\s+\d{4}\s+` + // date: DD Mon YYYY
		`\d{2}:\d{2}:\d{2}\.\d+\s+` + // time: HH:MM:SS.mmm
		`([.\-*#])\s+` + // level char
		`(.+)$`, // message
)

// Quick check pattern - just looks for the basic structure
var quickCheckPattern = regexp.MustCompile(`^\d+:[MSCX]\s+\d`)

// IsRedisLog checks if a log line appears to be from Redis.
func IsRedisLog(line string) bool {
	if len(line) < 20 {
		return false
	}
	return quickCheckPattern.MatchString(line)
}

// ParseRedisLog parses a Redis log line and returns structured info.
func ParseRedisLog(line string) *RedisLogInfo {
	matches := redisLogPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	info := &RedisLogInfo{
		PID:       matches[1],
		Role:      matches[2],
		LevelChar: matches[3],
		Message:   matches[4],
	}

	// Map role to human-readable name
	switch info.Role {
	case "M":
		info.RoleName = "master"
	case "S":
		info.RoleName = "slave"
	case "C":
		info.RoleName = "child"
	case "X":
		info.RoleName = "sentinel"
	default:
		info.RoleName = info.Role
	}

	// Map level char to name
	switch info.LevelChar {
	case ".":
		info.LevelName = "debug"
	case "-":
		info.LevelName = "verbose"
	case "*":
		info.LevelName = "notice"
	case "#":
		info.LevelName = "warning"
	default:
		info.LevelName = "info"
	}

	return info
}

// SeverityFromLevel converts Redis log level to OTEL severity.
// Returns (severity_number, severity_text).
func SeverityFromLevel(levelChar string) (int, string) {
	switch levelChar {
	case ".": // debug
		return 5, "DEBUG"
	case "-": // verbose
		return 5, "DEBUG"
	case "*": // notice
		return 9, "INFO"
	case "#": // warning
		return 13, "WARN"
	default:
		return 9, "INFO"
	}
}

// BuildCleanedMessage creates a human-readable message from parsed fields.
// Format: [{role}] {message}
func BuildCleanedMessage(info *RedisLogInfo) string {
	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(info.RoleName)
	sb.WriteString("] ")
	sb.WriteString(info.Message)
	return sb.String()
}
