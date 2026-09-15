// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cronlogprocessor

import (
	"regexp"
	"strings"
)

// CronLogInfo holds parsed cron log information.
type CronLogInfo struct {
	Type    string // "cmd", "cmd_end", "session_open", "session_close"
	User    string // The user running the cron job
	Command string // The command being executed (for cmd type)
	Message string // Clean message for body rewriting
}

// Regular expressions for cron log parsing
var (
	// CMD execution: (root) CMD (command)
	// Non-anchored to match embedded in syslog messages like:
	// "1 2025-12-31T10:05:00 host /usr/sbin/cron 19467 - - (root) CMD (/usr/libexec/atrun)"
	cmdPattern = regexp.MustCompile(`\(([^)]+)\)\s+CMD\s+\((.+)\)$`)

	// CMDEND execution: (root) CMDEND (run-parts /etc/cron.hourly)
	// Logged by crond when a command finishes execution
	cmdEndPattern = regexp.MustCompile(`\(([^)]+)\)\s+CMDEND\s+\((.+)\)$`)

	// PAM session opened: pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)
	pamOpenPattern = regexp.MustCompile(`pam_unix\(cron:session\):\s+session opened for user (\w+)`)

	// PAM session closed: pam_unix(cron:session): session closed for user root
	pamClosePattern = regexp.MustCompile(`pam_unix\(cron:session\):\s+session closed for user (\w+)`)

	// run-parts starting: (/etc/cron.hourly) starting 0anacron
	// This is logged by CROND when run-parts executes scripts in cron directories
	runPartsPattern = regexp.MustCompile(`^\((/etc/cron\.[^)]+)\)\s+starting\s+(\S+)$`)

	// run-parts finished: (/etc/cron.hourly) finished 0anacron
	runPartsFinishedPattern = regexp.MustCompile(`^\((/etc/cron\.[^)]+)\)\s+finished\s+(\S+)$`)
)

// IsCronLog checks if the message looks like a cron log entry.
func IsCronLog(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}

	// Check for CMD pattern
	if cmdPattern.MatchString(body) {
		return true
	}

	// Check for CMDEND pattern
	if cmdEndPattern.MatchString(body) {
		return true
	}

	// Check for PAM session patterns
	if strings.Contains(body, "pam_unix(cron:session):") {
		return true
	}

	// Check for run-parts patterns (cron directory execution)
	if runPartsPattern.MatchString(body) || runPartsFinishedPattern.MatchString(body) {
		return true
	}

	return false
}

// ParseCronLog parses a cron log message and extracts structured fields.
func ParseCronLog(body string) *CronLogInfo {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	// Try CMD pattern first
	if matches := cmdPattern.FindStringSubmatch(body); matches != nil {
		user := matches[1]
		command := matches[2]

		// Clean up command - remove output redirection for cleaner display
		cleanCmd := cleanCommand(command)

		return &CronLogInfo{
			Type:    "cmd",
			User:    user,
			Command: command,
			Message: "cron: " + user + " " + cleanCmd,
		}
	}

	// Try CMDEND pattern (command finished)
	if matches := cmdEndPattern.FindStringSubmatch(body); matches != nil {
		user := matches[1]
		command := matches[2]

		// Clean up command for display
		cleanCmd := cleanCommand(command)

		return &CronLogInfo{
			Type:    "cmd_end",
			User:    user,
			Command: command,
			Message: "cron finished: " + user + " " + cleanCmd,
		}
	}

	// Try PAM session opened
	if matches := pamOpenPattern.FindStringSubmatch(body); matches != nil {
		return &CronLogInfo{
			Type:    "session_open",
			User:    matches[1],
			Message: "cron session opened: " + matches[1],
		}
	}

	// Try PAM session closed
	if matches := pamClosePattern.FindStringSubmatch(body); matches != nil {
		return &CronLogInfo{
			Type:    "session_close",
			User:    matches[1],
			Message: "cron session closed: " + matches[1],
		}
	}

	// Try run-parts starting pattern: (/etc/cron.hourly) starting 0anacron
	if matches := runPartsPattern.FindStringSubmatch(body); matches != nil {
		cronDir := matches[1]
		script := matches[2]
		return &CronLogInfo{
			Type:    "run_parts_start",
			Command: script,
			Message: "cron: " + cronDir + " starting " + script,
		}
	}

	// Try run-parts finished pattern: (/etc/cron.hourly) finished 0anacron
	if matches := runPartsFinishedPattern.FindStringSubmatch(body); matches != nil {
		cronDir := matches[1]
		script := matches[2]
		return &CronLogInfo{
			Type:    "run_parts_finish",
			Command: script,
			Message: "cron: " + cronDir + " finished " + script,
		}
	}

	return nil
}

// cleanCommand simplifies a cron command for display.
// Removes common noise like output redirection, flock wrappers.
func cleanCommand(cmd string) string {
	// Remove trailing output redirection
	cmd = regexp.MustCompile(`\s*>\s*/dev/null\s*2>&1\s*$`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`\s*2>&1\s*>\s*/dev/null\s*$`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`\s*>\s*/dev/null\s*$`).ReplaceAllString(cmd, "")

	// Remove outer parentheses if they wrap the whole command
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, "(") && strings.HasSuffix(cmd, ")") {
		inner := cmd[1 : len(cmd)-1]
		// Only remove if balanced
		if countChar(inner, '(') == countChar(inner, ')') {
			cmd = inner
		}
	}

	// Extract command from flock wrapper: flock -n -E 0 -o /tmp/lock.lock /path/to/script
	if strings.Contains(cmd, "flock") {
		flockPattern := regexp.MustCompile(`(?:/usr/(?:local/)?bin/)?flock\s+[^/]*\s+/\S+\.lock\s+(.+)$`)
		if matches := flockPattern.FindStringSubmatch(cmd); matches != nil {
			cmd = matches[1]
		}
	}

	return strings.TrimSpace(cmd)
}

func countChar(s string, c rune) int {
	count := 0
	for _, ch := range s {
		if ch == c {
			count++
		}
	}
	return count
}
