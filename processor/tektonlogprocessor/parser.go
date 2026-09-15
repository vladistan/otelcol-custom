// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tektonlogprocessor

import (
	"regexp"
	"strings"
)

// TektonLogInfo holds parsed information from Tekton log messages.
type TektonLogInfo struct {
	// For Event messages
	EventType   string // Normal, Warning
	EventReason string // Succeeded, Failed, Started, etc.

	// For ObjectReference in Event messages
	ObjectKind       string // PipelineRun, TaskRun, etc.
	ObjectNamespace  string
	ObjectName       string
	ObjectUID        string
	ObjectAPIVersion string

	// For PipelineRun/TaskRun status messages
	ResourceType string // PipelineRun, TaskRun
	ResourceName string
	Status       string // Succeeded, Failed, Running
	StatusReason string // Human-readable status

	// Task statistics
	TasksCompleted int
	TasksFailed    int
	TasksCancelled int
	TasksSkipped   int

	// Clean message for body
	CleanMessage string
}

// Patterns for parsing Tekton logs
var (
	// Event with ObjectReference pattern
	// Event(v1.ObjectReference{Kind:"PipelineRun", Namespace:"local-cert-bot", Name:"xxx", UID:"xxx", APIVersion:"tekton.dev/v1", ResourceVersion:"xxx", FieldPath:""}): type: 'Normal' reason: 'Succeeded' Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0
	eventObjectRefPattern = regexp.MustCompile(`^Event\(v1\.ObjectReference\{([^}]+)\}\):\s*type:\s*'(\w+)'\s*reason:\s*'(\w+)'\s*(.*)$`)

	// ObjectReference field extraction
	objRefKindPattern       = regexp.MustCompile(`Kind:\\?"(\w+)\\?"`)
	objRefNamespacePattern  = regexp.MustCompile(`Namespace:\\?"([^\\\"]+)\\?"`)
	objRefNamePattern       = regexp.MustCompile(`Name:\\?"([^\\\"]+)\\?"`)
	objRefUIDPattern        = regexp.MustCompile(`UID:\\?"([^\\\"]+)\\?"`)
	objRefAPIVersionPattern = regexp.MustCompile(`APIVersion:\\?"([^\\\"]+)\\?"`)

	// PipelineRun/TaskRun status pattern
	// PipelineRun xxx status is being set to &{Succeeded True  {timestamp} Succeeded Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0}
	pipelineStatusPattern = regexp.MustCompile(`^(PipelineRun|TaskRun)\s+(\S+)\s+status is being set to\s+&\{(\w+)\s+(\w+)\s+\{[^}]+\}\s+(\w+)\s+(.*)?\}$`)

	// Task statistics pattern
	taskStatsPattern = regexp.MustCompile(`Tasks Completed:\s*(\d+)\s*\(Failed:\s*(\d+),\s*Cancelled\s*(\d+)\),\s*Skipped:\s*(\d+)`)
)

// IsTektonEvent checks if a log message is a Tekton Event with ObjectReference.
func IsTektonEvent(body string) bool {
	return strings.HasPrefix(body, "Event(v1.ObjectReference{")
}

// IsTektonStatus checks if a log message is a PipelineRun/TaskRun status message.
func IsTektonStatus(body string) bool {
	return (strings.HasPrefix(body, "PipelineRun ") || strings.HasPrefix(body, "TaskRun ")) &&
		strings.Contains(body, "status is being set to")
}

// ParseTektonEvent parses a Tekton Event message with ObjectReference.
func ParseTektonEvent(body string) *TektonLogInfo {
	matches := eventObjectRefPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &TektonLogInfo{
		EventType:   matches[2],
		EventReason: matches[3],
	}

	// Parse ObjectReference fields
	objRefStr := matches[1]
	if m := objRefKindPattern.FindStringSubmatch(objRefStr); m != nil {
		info.ObjectKind = m[1]
	}
	if m := objRefNamespacePattern.FindStringSubmatch(objRefStr); m != nil {
		info.ObjectNamespace = m[1]
	}
	if m := objRefNamePattern.FindStringSubmatch(objRefStr); m != nil {
		info.ObjectName = m[1]
	}
	if m := objRefUIDPattern.FindStringSubmatch(objRefStr); m != nil {
		info.ObjectUID = m[1]
	}
	if m := objRefAPIVersionPattern.FindStringSubmatch(objRefStr); m != nil {
		info.ObjectAPIVersion = m[1]
	}

	// Parse task statistics from the message part
	msgPart := matches[4]
	if m := taskStatsPattern.FindStringSubmatch(msgPart); m != nil {
		info.TasksCompleted = parseInt(m[1])
		info.TasksFailed = parseInt(m[2])
		info.TasksCancelled = parseInt(m[3])
		info.TasksSkipped = parseInt(m[4])
	}

	// Build clean message
	info.CleanMessage = buildEventMessage(info, msgPart)

	return info
}

// ParseTektonStatus parses a PipelineRun/TaskRun status message.
func ParseTektonStatus(body string) *TektonLogInfo {
	matches := pipelineStatusPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &TektonLogInfo{
		ResourceType: matches[1],
		ResourceName: matches[2],
		Status:       matches[3],
		StatusReason: matches[5],
	}

	// Parse task statistics
	msgPart := matches[6]
	if m := taskStatsPattern.FindStringSubmatch(msgPart); m != nil {
		info.TasksCompleted = parseInt(m[1])
		info.TasksFailed = parseInt(m[2])
		info.TasksCancelled = parseInt(m[3])
		info.TasksSkipped = parseInt(m[4])
	}

	// Build clean message
	info.CleanMessage = buildStatusMessage(info)

	return info
}

func buildEventMessage(info *TektonLogInfo, extraMsg string) string {
	var sb strings.Builder

	// Format: "PipelineRun namespace/name: Normal/Succeeded - Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0"
	if info.ObjectKind != "" {
		sb.WriteString(info.ObjectKind)
		sb.WriteString(" ")
	}
	if info.ObjectNamespace != "" {
		sb.WriteString(info.ObjectNamespace)
		sb.WriteString("/")
	}
	if info.ObjectName != "" {
		sb.WriteString(info.ObjectName)
	}
	sb.WriteString(": ")
	sb.WriteString(info.EventType)
	sb.WriteString("/")
	sb.WriteString(info.EventReason)

	// Add task stats if present
	if info.TasksCompleted > 0 || info.TasksFailed > 0 || info.TasksCancelled > 0 || info.TasksSkipped > 0 {
		sb.WriteString(" - ")
		sb.WriteString(formatTaskStats(info))
	} else if extraMsg != "" {
		// Use the extra message if no task stats
		extraMsg = strings.TrimSpace(extraMsg)
		if extraMsg != "" && !strings.HasPrefix(extraMsg, "Tasks Completed") {
			sb.WriteString(" - ")
			sb.WriteString(extraMsg)
		}
	}

	return sb.String()
}

func buildStatusMessage(info *TektonLogInfo) string {
	var sb strings.Builder

	// Format: "PipelineRun name: Succeeded - Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0"
	sb.WriteString(info.ResourceType)
	sb.WriteString(" ")
	sb.WriteString(info.ResourceName)
	sb.WriteString(": ")
	sb.WriteString(info.Status)

	// Add task stats if present
	if info.TasksCompleted > 0 || info.TasksFailed > 0 || info.TasksCancelled > 0 || info.TasksSkipped > 0 {
		sb.WriteString(" - ")
		sb.WriteString(formatTaskStats(info))
	}

	return sb.String()
}

func formatTaskStats(info *TektonLogInfo) string {
	var sb strings.Builder
	sb.WriteString("Tasks: ")
	sb.WriteString(itoa(info.TasksCompleted))
	sb.WriteString(" completed")
	if info.TasksFailed > 0 {
		sb.WriteString(", ")
		sb.WriteString(itoa(info.TasksFailed))
		sb.WriteString(" failed")
	}
	if info.TasksCancelled > 0 {
		sb.WriteString(", ")
		sb.WriteString(itoa(info.TasksCancelled))
		sb.WriteString(" cancelled")
	}
	if info.TasksSkipped > 0 {
		sb.WriteString(", ")
		sb.WriteString(itoa(info.TasksSkipped))
		sb.WriteString(" skipped")
	}
	return sb.String()
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
