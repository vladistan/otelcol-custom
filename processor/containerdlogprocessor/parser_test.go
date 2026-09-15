// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containerdlogprocessor

import (
	"testing"
)

func TestIsContainerdLog(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "Valid containerd log",
			body:     `time="2025-12-20T03:15:21.415044815Z" level=error msg="ttrpc: received message on inactive stream" stream=2591`,
			expected: true,
		},
		{
			name:     "Valid info log",
			body:     `time="2025-12-20T00:36:52.452501259Z" level=info msg="shim disconnected" id=655b77c4cc5f58bc3077cf504c9949194e359bc3595ba38024e707cf944ff187 namespace=moby`,
			expected: true,
		},
		{
			name:     "Not containerd log",
			body:     "some random log message",
			expected: false,
		},
		{
			name:     "Empty message",
			body:     "",
			expected: false,
		},
		{
			name:     "JSON log",
			body:     `{"level":"info","msg":"hello"}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsContainerdLog(tt.body)
			if result != tt.expected {
				t.Errorf("IsContainerdLog(%q) = %v, want %v", tt.body, result, tt.expected)
			}
		})
	}
}

func TestParseContainerdLog(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		expectedLevel     string
		expectedMessage   string
		expectedContainer string
		expectedNamespace string
		expectedEventType string
		expectedImage     string
		expectedError     string
		expectedCleanMsg  string
	}{
		{
			name:             "ttrpc inactive stream error",
			body:             `time="2025-12-20T03:15:21.415044815Z" level=error msg="ttrpc: received message on inactive stream" stream=2591`,
			expectedLevel:    "error",
			expectedMessage:  "ttrpc: received message on inactive stream",
			expectedCleanMsg: "containerd: ttrpc inactive stream 2591",
		},
		{
			name:              "shim disconnected",
			body:              `time="2025-12-20T00:36:52.452501259Z" level=info msg="shim disconnected" id=655b77c4cc5f58bc3077cf504c9949194e359bc3595ba38024e707cf944ff187 namespace=moby`,
			expectedLevel:     "info",
			expectedMessage:   "shim disconnected",
			expectedContainer: "655b77c4cc5f58bc3077cf504c9949194e359bc3595ba38024e707cf944ff187", // pragma: allowlist secret
			expectedNamespace: "moby",
			expectedCleanMsg:  "containerd: shim disconnect 655b77c4cc5f",
		},
		{
			name:              "container event discarded - deleted",
			body:              `time="2026-01-02T04:18:04.077043798Z" level=info msg="container event discarded" container=42620fadf6482c2b4dd79d34b4a58a0f21d15c89b55b2c5147315cb75af7649c type=CONTAINER_DELETED_EVENT`,
			expectedLevel:     "info",
			expectedMessage:   "container event discarded",
			expectedContainer: "42620fadf6482c2b4dd79d34b4a58a0f21d15c89b55b2c5147315cb75af7649c", // pragma: allowlist secret
			expectedEventType: "CONTAINER_DELETED_EVENT",
			expectedCleanMsg:  "containerd: container deleted 42620fadf648",
		},
		{
			name:              "container event discarded - stopped",
			body:              `time="2026-01-02T04:17:37.966197690Z" level=info msg="container event discarded" container=9339379c86cbeb245df1b8f8a76d6b83c2c7c9a24685b5769cb986aa0690c918 type=CONTAINER_STOPPED_EVENT`,
			expectedLevel:     "info",
			expectedMessage:   "container event discarded",
			expectedContainer: "9339379c86cbeb245df1b8f8a76d6b83c2c7c9a24685b5769cb986aa0690c918", // pragma: allowlist secret
			expectedEventType: "CONTAINER_STOPPED_EVENT",
			expectedCleanMsg:  "containerd: container stopped 9339379c86cb",
		},
		{
			name:             "PullImage",
			body:             `time="2026-01-02T04:12:40.985824409Z" level=info msg="PullImage \"123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53\""`,
			expectedLevel:    "info",
			expectedMessage:  `PullImage "123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53"`,
			expectedImage:    "123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53",
			expectedCleanMsg: "containerd: pulling 123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53",
		},
		{
			name:              "StopPodSandbox",
			body:              `time="2026-01-02T04:13:04.033593647Z" level=info msg="StopPodSandbox for \"42620fadf6482c2b4dd79d34b4a58a0f21d15c89b55b2c5147315cb75af7649c\""`,
			expectedLevel:     "info",
			expectedMessage:   `StopPodSandbox for "42620fadf6482c2b4dd79d34b4a58a0f21d15c89b55b2c5147315cb75af7649c"`, // pragma: allowlist secret
			expectedContainer: "42620fadf6482c2b4dd79d34b4a58a0f21d15c89b55b2c5147315cb75af7649c",                      // pragma: allowlist secret
			expectedCleanMsg:  "containerd: stopping sandbox 42620fadf648",
		},
		{
			name:             "context deadline exceeded",
			body:             `time="2025-12-20T03:14:24.429611058Z" level=error msg="post event" error="context deadline exceeded"`,
			expectedLevel:    "error",
			expectedMessage:  "post event",
			expectedError:    "context deadline exceeded",
			expectedCleanMsg: "containerd: post event failed: context deadline exceeded",
		},
		{
			name:             "forward event error",
			body:             `time="2025-12-20T03:14:23.977892841Z" level=error msg="forward event" error="context deadline exceeded"`,
			expectedLevel:    "error",
			expectedMessage:  "forward event",
			expectedError:    "context deadline exceeded",
			expectedCleanMsg: "containerd: forward event failed: context deadline exceeded",
		},
		{
			name:             "warning unknown status",
			body:             `time="2025-12-20T03:13:14.783376669Z" level=warning msg="unknown status" status=0`,
			expectedLevel:    "warning",
			expectedMessage:  "unknown status",
			expectedCleanMsg: "containerd: unknown status 0",
		},
		{
			name:              "connecting to shim",
			body:              `time="2025-12-20T00:41:42.524282418Z" level=info msg="connecting to shim e1c6cbcbde986dfa415b036f728ca00725a979b696308d9eb326c9c20f26f45b" address="unix:///run/containerd/s/7944451131eb098c4a1ba29a3e1c6c20483b85e0730e1307177dc969ea4b53c1" namespace=moby protocol=ttrpc version=3`,
			expectedLevel:     "info",
			expectedMessage:   "connecting to shim e1c6cbcbde986dfa415b036f728ca00725a979b696308d9eb326c9c20f26f45b",
			expectedNamespace: "moby",
			expectedCleanMsg:  "containerd: shim connect e1c6cbcbde98",
		},
		{
			name:              "RemoveContainer",
			body:              `time="2025-12-20T00:00:52.501220351Z" level=info msg="RemoveContainer for \"08f69bc3705d43d218f8572290f40a159b2cf30c48013bdc254f68a8bc6b24be\""`,
			expectedLevel:     "info",
			expectedMessage:   `RemoveContainer for "08f69bc3705d43d218f8572290f40a159b2cf30c48013bdc254f68a8bc6b24be"`, // pragma: allowlist secret
			expectedContainer: "08f69bc3705d43d218f8572290f40a159b2cf30c48013bdc254f68a8bc6b24be",                       // pragma: allowlist secret
			expectedCleanMsg:  "containerd: removing container 08f69bc3705d",
		},
		{
			name:              "get state error",
			body:              `time="2025-12-20T03:13:14.782287509Z" level=error msg="get state for 015cfab2e6abc99ca03ba3155d428dd6c095fd1994f1adcc603d08f563ed85c4" error="context deadline exceeded"`,
			expectedLevel:     "error",
			expectedMessage:   "get state for 015cfab2e6abc99ca03ba3155d428dd6c095fd1994f1adcc603d08f563ed85c4",
			expectedContainer: "015cfab2e6abc99ca03ba3155d428dd6c095fd1994f1adcc603d08f563ed85c4", // pragma: allowlist secret
			expectedError:     "context deadline exceeded",
			expectedCleanMsg:  "containerd: get state 015cfab2e6ab failed: context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseContainerdLog(tt.body)

			if result == nil {
				t.Fatalf("ParseContainerdLog(%q) = nil, want non-nil", tt.body)
			}

			if result.Level != tt.expectedLevel {
				t.Errorf("Level = %q, want %q", result.Level, tt.expectedLevel)
			}
			if result.Message != tt.expectedMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.expectedMessage)
			}
			if tt.expectedContainer != "" && result.ContainerID != tt.expectedContainer {
				t.Errorf("ContainerID = %q, want %q", result.ContainerID, tt.expectedContainer)
			}
			if tt.expectedNamespace != "" && result.Namespace != tt.expectedNamespace {
				t.Errorf("Namespace = %q, want %q", result.Namespace, tt.expectedNamespace)
			}
			if tt.expectedEventType != "" && result.EventType != tt.expectedEventType {
				t.Errorf("EventType = %q, want %q", result.EventType, tt.expectedEventType)
			}
			if tt.expectedImage != "" && result.Image != tt.expectedImage {
				t.Errorf("Image = %q, want %q", result.Image, tt.expectedImage)
			}
			if tt.expectedError != "" && result.Error != tt.expectedError {
				t.Errorf("Error = %q, want %q", result.Error, tt.expectedError)
			}
			if tt.expectedCleanMsg != "" && result.CleanMsg != tt.expectedCleanMsg {
				t.Errorf("CleanMsg = %q, want %q", result.CleanMsg, tt.expectedCleanMsg)
			}
		})
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		id       string
		expected string
	}{
		{"655b77c4cc5f58bc3077cf504c9949194e359bc3595ba38024e707cf944ff187", "655b77c4cc5f"}, // pragma: allowlist secret
		{"short", "short"},
		{"123456789012", "123456789012"},  // pragma: allowlist secret
		{"1234567890123", "123456789012"}, // pragma: allowlist secret
	}

	for _, tt := range tests {
		result := shortID(tt.id)
		if result != tt.expected {
			t.Errorf("shortID(%q) = %q, want %q", tt.id, result, tt.expected)
		}
	}
}

func TestIsContainerID(t *testing.T) {
	tests := []struct {
		id       string
		expected bool
	}{
		{"655b77c4cc5f58bc3077cf504c9949194e359bc3595ba38024e707cf944ff187", true}, // pragma: allowlist secret
		{"abcdef123456", true}, // pragma: allowlist secret
		{"short", false},
		{"not-hex-value", false},
		{"ABCDEF123456", false}, // pragma: allowlist secret (uppercase variant)
	}

	for _, tt := range tests {
		result := isContainerID(tt.id)
		if result != tt.expected {
			t.Errorf("isContainerID(%q) = %v, want %v", tt.id, result, tt.expected)
		}
	}
}

func TestParsePodSandboxMetadata(t *testing.T) {
	tests := []struct {
		name                 string
		body                 string
		expectedPodName      string
		expectedPodUID       string
		expectedPodNamespace string
		expectedPodAttempt   string
		expectedCleanMsg     string
	}{
		{
			name:                 "RunPodSandbox with PodSandboxMetadata",
			body:                 `time="2026-01-02T04:36:01.085444170Z" level=info msg="RunPodSandbox for &PodSandboxMetadata{Name:otel-main-0,Uid:ad2d65a1-447b-403b-a2b0-7bf583c60863,Namespace:opentelemetry,Attempt:0,}"`,
			expectedPodName:      "otel-main-0",
			expectedPodUID:       "ad2d65a1-447b-403b-a2b0-7bf583c60863",
			expectedPodNamespace: "opentelemetry",
			expectedPodAttempt:   "0",
			expectedCleanMsg:     "containerd: sandbox otel-main-0 (opentelemetry)",
		},
		{
			name:                 "RunPodSandbox returns sandbox id",
			body:                 `time="2026-01-02T04:36:01.143798046Z" level=info msg="RunPodSandbox for &PodSandboxMetadata{Name:otel-main-0,Uid:ad2d65a1-447b-403b-a2b0-7bf583c60863,Namespace:opentelemetry,Attempt:0,} returns sandbox id \"d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3\""`,
			expectedPodName:      "otel-main-0",
			expectedPodUID:       "ad2d65a1-447b-403b-a2b0-7bf583c60863",
			expectedPodNamespace: "opentelemetry",
			expectedPodAttempt:   "0",
			expectedCleanMsg:     "containerd: sandbox otel-main-0 (opentelemetry) -> d7fffcf9b01c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseContainerdLog(tt.body)

			if result == nil {
				t.Fatalf("ParseContainerdLog(%q) = nil, want non-nil", tt.body)
			}

			if result.PodName != tt.expectedPodName {
				t.Errorf("PodName = %q, want %q", result.PodName, tt.expectedPodName)
			}
			if result.PodUID != tt.expectedPodUID {
				t.Errorf("PodUID = %q, want %q", result.PodUID, tt.expectedPodUID)
			}
			if result.PodNamespace != tt.expectedPodNamespace {
				t.Errorf("PodNamespace = %q, want %q", result.PodNamespace, tt.expectedPodNamespace)
			}
			if result.PodAttempt != tt.expectedPodAttempt {
				t.Errorf("PodAttempt = %q, want %q", result.PodAttempt, tt.expectedPodAttempt)
			}
			if tt.expectedCleanMsg != "" && result.CleanMsg != tt.expectedCleanMsg {
				t.Errorf("CleanMsg = %q, want %q", result.CleanMsg, tt.expectedCleanMsg)
			}
		})
	}
}

func TestParseContainerMetadata(t *testing.T) {
	tests := []struct {
		name                     string
		body                     string
		expectedContainerName    string
		expectedContainerAttempt string
		expectedCleanMsg         string
	}{
		{
			name:                     "CreateContainer with ContainerMetadata",
			body:                     `time="2026-01-02T04:36:01.298477797Z" level=info msg="CreateContainer within sandbox \"d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3\" for &ContainerMetadata{Name:otel-main,Attempt:0,}"`,
			expectedContainerName:    "otel-main",
			expectedContainerAttempt: "0",
			expectedCleanMsg:         "containerd: create otel-main (d7fffcf9b01c)",
		},
		{
			name:                     "CreateContainer with both Pod and Container metadata",
			body:                     `time="2026-01-02T04:36:01.298477797Z" level=info msg="CreateContainer within sandbox \"d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3\" for container &ContainerMetadata{Name:init-fs,Attempt:0,} within &PodSandboxMetadata{Name:otel-main-0,Uid:ad2d65a1-447b-403b-a2b0-7bf583c60863,Namespace:opentelemetry,Attempt:0,}"`,
			expectedContainerName:    "init-fs",
			expectedContainerAttempt: "0",
			expectedCleanMsg:         "containerd: create init-fs in otel-main-0 (d7fffcf9b01c)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseContainerdLog(tt.body)

			if result == nil {
				t.Fatalf("ParseContainerdLog(%q) = nil, want non-nil", tt.body)
			}

			if result.ContainerName != tt.expectedContainerName {
				t.Errorf("ContainerName = %q, want %q", result.ContainerName, tt.expectedContainerName)
			}
			if result.ContainerAttempt != tt.expectedContainerAttempt {
				t.Errorf("ContainerAttempt = %q, want %q", result.ContainerAttempt, tt.expectedContainerAttempt)
			}
			if tt.expectedCleanMsg != "" && result.CleanMsg != tt.expectedCleanMsg {
				t.Errorf("CleanMsg = %q, want %q", result.CleanMsg, tt.expectedCleanMsg)
			}
		})
	}
}

func TestStartContainer(t *testing.T) {
	body := `time="2026-01-02T04:36:01.500000000Z" level=info msg="StartContainer for \"d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3\""`
	result := ParseContainerdLog(body)

	if result == nil {
		t.Fatalf("ParseContainerdLog(%q) = nil, want non-nil", body)
	}

	if result.ContainerID != "d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3" { // pragma: allowlist secret
		t.Errorf("ContainerID = %q, want %q", result.ContainerID, "d7fffcf9b01c4cd55cef9839e2fa7a28e3e4b9f3f1e36e39af0dce9583dc15a3") // pragma: allowlist secret
	}

	expectedCleanMsg := "containerd: start d7fffcf9b01c"
	if result.CleanMsg != expectedCleanMsg {
		t.Errorf("CleanMsg = %q, want %q", result.CleanMsg, expectedCleanMsg)
	}
}

func TestPulledImage(t *testing.T) {
	body := `time="2026-01-02T04:36:04.100000000Z" level=info msg="Pulled image \"123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53\" with image id \"sha256:abc123\", repo tag \"v0.1.53\", repo digest \"sha256:def456\", size 50000000 in 3.5s"`
	result := ParseContainerdLog(body)

	if result == nil {
		t.Fatalf("ParseContainerdLog(%q) = nil, want non-nil", body)
	}

	expectedImage := "123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53"
	if result.Image != expectedImage {
		t.Errorf("Image = %q, want %q", result.Image, expectedImage)
	}

	expectedCleanMsg := "containerd: pulled 123456789012.dkr.ecr.us-east-1.amazonaws.com/otelcol-gateway:v0.1.53"
	if result.CleanMsg != expectedCleanMsg {
		t.Errorf("CleanMsg = %q, want %q", result.CleanMsg, expectedCleanMsg)
	}
}
