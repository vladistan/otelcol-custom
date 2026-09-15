// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tektonlogprocessor

import (
	"testing"
)

func TestIsTektonEvent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Event with ObjectReference",
			input:    `Event(v1.ObjectReference{Kind:"PipelineRun", Namespace:"local-cert-bot", Name:"tekton-local-cert-bot-pipeline-run-lt7w5", UID:"9477e900-eafc-494d-af9e-831203f335a3", APIVersion:"tekton.dev/v1", ResourceVersion:"6744138", FieldPath:""}): type: 'Normal' reason: 'Succeeded' Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0`,
			expected: true,
		},
		{
			name:     "Event with escaped quotes",
			input:    `Event(v1.ObjectReference{Kind:\"PipelineRun\", Namespace:\"default\", Name:\"test-run\", UID:\"abc-123\", APIVersion:\"tekton.dev/v1\", ResourceVersion:\"123\", FieldPath:\"\"}): type: 'Normal' reason: 'Started' Pipeline started`,
			expected: true,
		},
		{
			name:     "Not an event - regular log",
			input:    `PipelineRun test-run status is being set to &{Succeeded True {2026-01-02} Succeeded Tasks Completed: 1}`,
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTektonEvent(tt.input)
			if result != tt.expected {
				t.Errorf("IsTektonEvent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsTektonStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "PipelineRun status",
			input:    `PipelineRun tekton-local-cert-bot-pipeline-run-lt7w5 status is being set to &{Succeeded True  {2026-01-02 23:47:35.211593321 +0000 UTC m=+292581.877320485} Succeeded Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0}`,
			expected: true,
		},
		{
			name:     "TaskRun status",
			input:    `TaskRun my-task-run status is being set to &{Succeeded True {2026-01-02} Succeeded }`,
			expected: true,
		},
		{
			name:     "Not a status - event message",
			input:    `Event(v1.ObjectReference{Kind:"PipelineRun"}): type: 'Normal' reason: 'Succeeded'`,
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTektonStatus(tt.input)
			if result != tt.expected {
				t.Errorf("IsTektonStatus() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseTektonEvent(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectNil         bool
		expectedKind      string
		expectedNamespace string
		expectedName      string
		expectedEventType string
		expectedReason    string
		expectedCompleted int
		expectedFailed    int
	}{
		{
			name:              "Full event with task stats",
			input:             `Event(v1.ObjectReference{Kind:"PipelineRun", Namespace:"local-cert-bot", Name:"tekton-local-cert-bot-pipeline-run-lt7w5", UID:"9477e900-eafc-494d-af9e-831203f335a3", APIVersion:"tekton.dev/v1", ResourceVersion:"6744138", FieldPath:""}): type: 'Normal' reason: 'Succeeded' Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0`,
			expectNil:         false,
			expectedKind:      "PipelineRun",
			expectedNamespace: "local-cert-bot",
			expectedName:      "tekton-local-cert-bot-pipeline-run-lt7w5",
			expectedEventType: "Normal",
			expectedReason:    "Succeeded",
			expectedCompleted: 1,
			expectedFailed:    0,
		},
		{
			name:              "Event with escaped quotes",
			input:             `Event(v1.ObjectReference{Kind:\"TaskRun\", Namespace:\"default\", Name:\"build-task-abc\", UID:\"xyz-789\", APIVersion:\"tekton.dev/v1\", ResourceVersion:\"456\", FieldPath:\"\"}): type: 'Warning' reason: 'Failed' Task failed`,
			expectNil:         false,
			expectedKind:      "TaskRun",
			expectedNamespace: "default",
			expectedName:      "build-task-abc",
			expectedEventType: "Warning",
			expectedReason:    "Failed",
		},
		{
			name:      "Invalid format",
			input:     `Not an event message`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseTektonEvent(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseTektonEvent() expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseTektonEvent() returned nil, expected non-nil")
			}

			if result.ObjectKind != tt.expectedKind {
				t.Errorf("ObjectKind = %q, want %q", result.ObjectKind, tt.expectedKind)
			}
			if result.ObjectNamespace != tt.expectedNamespace {
				t.Errorf("ObjectNamespace = %q, want %q", result.ObjectNamespace, tt.expectedNamespace)
			}
			if result.ObjectName != tt.expectedName {
				t.Errorf("ObjectName = %q, want %q", result.ObjectName, tt.expectedName)
			}
			if result.EventType != tt.expectedEventType {
				t.Errorf("EventType = %q, want %q", result.EventType, tt.expectedEventType)
			}
			if result.EventReason != tt.expectedReason {
				t.Errorf("EventReason = %q, want %q", result.EventReason, tt.expectedReason)
			}
			if tt.expectedCompleted > 0 && result.TasksCompleted != tt.expectedCompleted {
				t.Errorf("TasksCompleted = %d, want %d", result.TasksCompleted, tt.expectedCompleted)
			}

			// Check clean message is generated
			if result.CleanMessage == "" {
				t.Error("CleanMessage should not be empty")
			}
			t.Logf("CleanMessage: %s", result.CleanMessage)
		})
	}
}

func TestParseTektonStatus(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectNil         bool
		expectedType      string
		expectedName      string
		expectedStatus    string
		expectedCompleted int
	}{
		{
			name:              "PipelineRun succeeded",
			input:             `PipelineRun tekton-local-cert-bot-pipeline-run-lt7w5 status is being set to &{Succeeded True  {2026-01-02 23:47:35.211593321 +0000 UTC m=+292581.877320485} Succeeded Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0}`,
			expectNil:         false,
			expectedType:      "PipelineRun",
			expectedName:      "tekton-local-cert-bot-pipeline-run-lt7w5",
			expectedStatus:    "Succeeded",
			expectedCompleted: 1,
		},
		{
			name:           "TaskRun running",
			input:          `TaskRun build-task-xyz status is being set to &{Running True  {2026-01-02 23:47:35} Running }`,
			expectNil:      false,
			expectedType:   "TaskRun",
			expectedName:   "build-task-xyz",
			expectedStatus: "Running",
		},
		{
			name:      "Invalid format",
			input:     `Not a status message`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseTektonStatus(tt.input)

			if tt.expectNil {
				if result != nil {
					t.Errorf("ParseTektonStatus() expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseTektonStatus() returned nil, expected non-nil")
			}

			if result.ResourceType != tt.expectedType {
				t.Errorf("ResourceType = %q, want %q", result.ResourceType, tt.expectedType)
			}
			if result.ResourceName != tt.expectedName {
				t.Errorf("ResourceName = %q, want %q", result.ResourceName, tt.expectedName)
			}
			if result.Status != tt.expectedStatus {
				t.Errorf("Status = %q, want %q", result.Status, tt.expectedStatus)
			}
			if tt.expectedCompleted > 0 && result.TasksCompleted != tt.expectedCompleted {
				t.Errorf("TasksCompleted = %d, want %d", result.TasksCompleted, tt.expectedCompleted)
			}

			// Check clean message is generated
			if result.CleanMessage == "" {
				t.Error("CleanMessage should not be empty")
			}
			t.Logf("CleanMessage: %s", result.CleanMessage)
		})
	}
}

func TestCleanMessageFormat(t *testing.T) {
	// Test that clean messages are formatted nicely
	eventInput := `Event(v1.ObjectReference{Kind:"PipelineRun", Namespace:"local-cert-bot", Name:"my-pipeline-run", UID:"abc", APIVersion:"tekton.dev/v1", ResourceVersion:"123", FieldPath:""}): type: 'Normal' reason: 'Succeeded' Tasks Completed: 3 (Failed: 1, Cancelled 0), Skipped: 2`

	result := ParseTektonEvent(eventInput)
	if result == nil {
		t.Fatal("ParseTektonEvent returned nil")
	}

	// Should be something like: "PipelineRun local-cert-bot/my-pipeline-run: Normal/Succeeded - Tasks: 3 completed, 1 failed, 2 skipped"
	expectedContains := []string{
		"PipelineRun",
		"local-cert-bot/my-pipeline-run",
		"Normal/Succeeded",
		"3 completed",
		"1 failed",
		"2 skipped",
	}

	for _, expected := range expectedContains {
		if !containsStr(result.CleanMessage, expected) {
			t.Errorf("CleanMessage %q should contain %q", result.CleanMessage, expected)
		}
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
