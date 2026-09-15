// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containerdlogprocessor

import (
	"regexp"
	"strings"
)

// ContainerdLogInfo holds parsed containerd log information.
type ContainerdLogInfo struct {
	Timestamp   string            // Original timestamp from log
	Level       string            // Log level: info, warning, error, debug
	Message     string            // The msg= field value
	Error       string            // The error= field value (if present)
	ContainerID string            // Container ID (if present)
	Namespace   string            // Namespace (if present)
	EventType   string            // Event type for container events
	Image       string            // Image name for pull/create events
	Fields      map[string]string // All other key=value fields
	CleanMsg    string            // Human-readable message for body rewriting

	// K8s metadata extracted from PodSandboxMetadata/ContainerMetadata
	PodName      string // k8s.pod.name
	PodUID       string // k8s.pod.uid
	PodNamespace string // k8s.namespace.name
	PodAttempt   string // Sandbox attempt number

	ContainerName    string // k8s.container.name (from ContainerMetadata)
	ContainerAttempt string // Container attempt number
}

// Regular expressions for parsing logrus text format
var (
	// Main pattern: time="..." level=<level> msg="..."
	// Captures: timestamp, level, msg, and rest of line for additional fields
	logrusPattern = regexp.MustCompile(`^time="([^"]+)"\s+level=(\w+)\s+msg="((?:[^"\\]|\\.)*)"\s*(.*)$`)

	// Pattern for key=value pairs (handles both quoted and unquoted values)
	kvPattern = regexp.MustCompile(`(\w+)=(?:"((?:[^"\\]|\\.)*)"|(\S+))`)

	// Pattern for container ID in message (64 hex chars, optionally in quotes)
	// Matches: "for \"abc123...\"" or "shim abc123..." or "container=abc123"
	msgContainerIDPattern = regexp.MustCompile(`(?:for\s+\\?"?|shim\s+|container[=:]\s*\\?"?)([a-f0-9]{64})`)

	// Pattern for image name with registry/repo:tag format
	imageNamePattern = regexp.MustCompile(`"([a-zA-Z0-9][a-zA-Z0-9._-]*(?:/[a-zA-Z0-9._-]+)+:[a-zA-Z0-9._-]+)"`)

	// Pattern for PodSandboxMetadata Go struct in containerd logs
	// Matches: &PodSandboxMetadata{Name:otel-main-0,Uid:ad2d65a1-...,Namespace:opentelemetry,Attempt:0,}
	// or: PodSandboxMetadata{Name:otel-main-0,Uid:ad2d65a1-...,Namespace:opentelemetry,Attempt:0,}
	podSandboxMetadataPattern = regexp.MustCompile(`&?PodSandboxMetadata\{Name:([^,]+),Uid:([^,]+),Namespace:([^,]+),Attempt:(\d+),?\}`)

	// Pattern for ContainerMetadata Go struct in containerd logs
	// Matches: &ContainerMetadata{Name:otel-main,Attempt:0,}
	containerMetadataPattern = regexp.MustCompile(`&?ContainerMetadata\{Name:([^,]+),Attempt:(\d+),?\}`)

	// Pattern for container ID in "returned pod sandbox ID" messages
	// Matches: RunPodSandbox for ... returns sandbox id "abc123..."
	sandboxIDReturnPattern = regexp.MustCompile(`returns? sandbox id "([a-f0-9]{64})"`)

	// Pattern for "CreateContainer" messages with container info
	// Matches: CreateContainer within sandbox "abc123..." for &ContainerMetadata{...}
	createContainerSandboxPattern = regexp.MustCompile(`within sandbox "([a-f0-9]{64})"`)
)

// ParseContainerdLog parses a containerd log message in logrus text format.
func ParseContainerdLog(body string) *ContainerdLogInfo {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	matches := logrusPattern.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	info := &ContainerdLogInfo{
		Timestamp: matches[1],
		Level:     matches[2],
		Message:   unescapeQuoted(matches[3]),
		Fields:    make(map[string]string),
	}

	// Parse remaining key=value pairs
	rest := matches[4]
	kvMatches := kvPattern.FindAllStringSubmatch(rest, -1)
	for _, kv := range kvMatches {
		key := kv[1]
		value := kv[2] // quoted value
		if value == "" {
			value = kv[3] // unquoted value
		}
		value = unescapeQuoted(value)

		switch key {
		case "error":
			info.Error = value
		case "container", "id":
			if info.ContainerID == "" && isContainerID(value) {
				info.ContainerID = value
			}
		case "namespace":
			info.Namespace = value
		case "type":
			info.EventType = value
		case "name":
			// Could be image name
			if strings.Contains(value, ":") || strings.Contains(value, "/") {
				info.Image = cleanImageName(value)
			}
		default:
			info.Fields[key] = value
		}
	}

	// Extract container ID from message if not found in fields
	if info.ContainerID == "" {
		// Try to find 64-char hex ID in the message
		if idMatches := msgContainerIDPattern.FindStringSubmatch(info.Message); idMatches != nil {
			info.ContainerID = idMatches[1]
		}
	}

	// Extract sandbox ID from "returns sandbox id" messages
	if info.ContainerID == "" {
		if idMatches := sandboxIDReturnPattern.FindStringSubmatch(info.Message); idMatches != nil {
			info.ContainerID = idMatches[1]
		}
	}

	// Extract sandbox ID from "within sandbox" messages
	if info.ContainerID == "" {
		if idMatches := createContainerSandboxPattern.FindStringSubmatch(info.Message); idMatches != nil {
			info.ContainerID = idMatches[1]
		}
	}

	// Extract image from message if not found
	if info.Image == "" && (strings.Contains(info.Message, "PullImage") ||
		strings.Contains(info.Message, "Pulled image") ||
		strings.Contains(info.Message, "ImageCreate") ||
		strings.Contains(info.Message, "ImageUpdate")) {
		if imgMatches := imageNamePattern.FindStringSubmatch(info.Message); imgMatches != nil {
			info.Image = imgMatches[1]
		}
	}

	// Extract PodSandboxMetadata from message (K8s pod info)
	// Format: &PodSandboxMetadata{Name:pod-name,Uid:uuid,Namespace:ns,Attempt:0,}
	if podMatches := podSandboxMetadataPattern.FindStringSubmatch(info.Message); podMatches != nil {
		info.PodName = podMatches[1]
		info.PodUID = podMatches[2]
		info.PodNamespace = podMatches[3]
		info.PodAttempt = podMatches[4]
	}

	// Extract ContainerMetadata from message (K8s container info)
	// Format: &ContainerMetadata{Name:container-name,Attempt:0,}
	if containerMatches := containerMetadataPattern.FindStringSubmatch(info.Message); containerMatches != nil {
		info.ContainerName = containerMatches[1]
		info.ContainerAttempt = containerMatches[2]
	}

	// Generate clean message
	info.CleanMsg = generateCleanMessage(info)

	return info
}

// generateCleanMessage creates a human-readable message from parsed info.
func generateCleanMessage(info *ContainerdLogInfo) string {
	msg := info.Message

	// Simplify common messages
	switch {
	case strings.HasPrefix(msg, "connecting to shim"):
		// "connecting to shim <id>" -> "shim connect: <short_id>"
		// Extract ID from message "connecting to shim <id>"
		parts := strings.Fields(msg)
		if len(parts) >= 4 && isContainerID(parts[3]) {
			return "containerd: shim connect " + shortID(parts[3])
		}
		if info.ContainerID != "" {
			return "containerd: shim connect " + shortID(info.ContainerID)
		}
		return "containerd: " + msg

	case msg == "shim disconnected":
		if info.ContainerID != "" {
			return "containerd: shim disconnect " + shortID(info.ContainerID)
		}
		return "containerd: shim disconnected"

	case msg == "container event discarded":
		eventType := info.EventType
		// Clean up event type: CONTAINER_DELETED_EVENT -> deleted
		eventType = strings.TrimPrefix(eventType, "CONTAINER_")
		eventType = strings.TrimSuffix(eventType, "_EVENT")
		eventType = strings.ToLower(eventType)
		if info.ContainerID != "" {
			return "containerd: container " + eventType + " " + shortID(info.ContainerID)
		}
		return "containerd: container " + eventType

	case strings.HasPrefix(msg, "PullImage"):
		if info.Image != "" {
			return "containerd: pulling " + info.Image
		}
		return "containerd: " + msg

	case strings.HasPrefix(msg, "ImageCreate"):
		if info.Image != "" {
			return "containerd: image created " + info.Image
		}
		return "containerd: image created"

	case strings.HasPrefix(msg, "ImageUpdate"):
		if info.Image != "" {
			return "containerd: image updated " + info.Image
		}
		return "containerd: image updated"

	case strings.HasPrefix(msg, "StopPodSandbox"):
		if info.ContainerID != "" {
			return "containerd: stopping sandbox " + shortID(info.ContainerID)
		}
		return "containerd: stopping sandbox"

	case strings.HasPrefix(msg, "RemoveContainer"):
		if info.ContainerID != "" {
			return "containerd: removing container " + shortID(info.ContainerID)
		}
		return "containerd: removing container"

	case msg == "ttrpc: received message on inactive stream":
		if stream, ok := info.Fields["stream"]; ok {
			return "containerd: ttrpc inactive stream " + stream
		}
		return "containerd: ttrpc inactive stream"

	case strings.HasPrefix(msg, "get state for"):
		// Extract ID from "get state for <id>"
		containerID := info.ContainerID
		if containerID == "" {
			parts := strings.Fields(msg)
			if len(parts) >= 4 && isContainerID(parts[3]) {
				containerID = parts[3]
			}
		}
		if containerID != "" {
			if info.Error != "" {
				return "containerd: get state " + shortID(containerID) + " failed: " + info.Error
			}
			return "containerd: get state " + shortID(containerID)
		}
		return "containerd: " + msg

	case msg == "post event" || msg == "forward event":
		if info.Error != "" {
			return "containerd: " + msg + " failed: " + info.Error
		}
		return "containerd: " + msg

	case msg == "unknown status":
		if status, ok := info.Fields["status"]; ok {
			return "containerd: unknown status " + status
		}
		return "containerd: unknown status"

	case strings.HasPrefix(msg, "warnings while cleaning up dead shim"):
		if info.ContainerID != "" {
			return "containerd: cleanup shim " + shortID(info.ContainerID) + " with warnings"
		}
		return "containerd: cleanup shim with warnings"

	case strings.HasPrefix(msg, "received container exit event"):
		if info.ContainerID != "" {
			return "containerd: container exit " + shortID(info.ContainerID)
		}
		return "containerd: container exit"

	case strings.HasPrefix(msg, "RunPodSandbox"):
		// RunPodSandbox for &PodSandboxMetadata{...} -> sandbox pod-name (ns)
		if info.PodName != "" {
			result := "containerd: sandbox " + info.PodName
			if info.PodNamespace != "" {
				result += " (" + info.PodNamespace + ")"
			}
			if strings.Contains(msg, "returns sandbox id") && info.ContainerID != "" {
				result += " -> " + shortID(info.ContainerID)
			}
			return result
		}
		return "containerd: " + msg

	case strings.HasPrefix(msg, "CreateContainer"):
		// CreateContainer within sandbox ... for &ContainerMetadata{...}
		result := "containerd: create container"
		if info.ContainerName != "" {
			result = "containerd: create " + info.ContainerName
		}
		if info.PodName != "" {
			result += " in " + info.PodName
		}
		if info.ContainerID != "" {
			result += " (" + shortID(info.ContainerID) + ")"
		}
		return result

	case strings.HasPrefix(msg, "StartContainer"):
		// StartContainer for <container-id>
		result := "containerd: start container"
		if info.ContainerID != "" {
			result = "containerd: start " + shortID(info.ContainerID)
		}
		return result

	case strings.HasPrefix(msg, "Pulled image"):
		// "Pulled image \"registry/image:tag\" with image id..." -> pulled image:tag
		if info.Image != "" {
			return "containerd: pulled " + info.Image
		}
		return "containerd: " + msg

	default:
		// For other messages, just prefix with containerd:
		if info.Error != "" {
			return "containerd: " + msg + " - " + info.Error
		}
		return "containerd: " + msg
	}
}

// shortID returns first 12 chars of container ID (standard Docker short ID).
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// isContainerID checks if a string looks like a container ID (hex string).
func isContainerID(s string) bool {
	if len(s) < 12 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// cleanImageName removes escape characters from image name.
func cleanImageName(name string) string {
	name = strings.ReplaceAll(name, "\\\"", "")
	name = strings.Trim(name, "\"")
	return name
}

// unescapeQuoted handles escaped characters in quoted strings.
func unescapeQuoted(s string) string {
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\t", "\t")
	return s
}

// IsContainerdLog checks if the body looks like a containerd log entry.
func IsContainerdLog(body string) bool {
	return strings.HasPrefix(body, "time=\"") && strings.Contains(body, "level=")
}
