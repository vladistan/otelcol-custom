// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type klogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *klogProcessor {
	return &klogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *klogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *klogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Klog processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *klogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *klogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		// Check if this resource matches our service name (if configured)
		if p.config.ServiceName != "" && !p.matchesServiceName(resourceAttrs) {
			continue
		}

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				p.processLogRecord(lr)
			}
		}
	}

	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

func (p *klogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
	// Check service.name
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		if serviceName == p.config.ServiceName {
			return true
		}
		if containsComponent(serviceName, p.config.ServiceName) {
			return true
		}
	}

	// Check container.name
	if val, ok := resourceAttrs.Get("container.name"); ok {
		containerName := val.Str()
		if containerName == p.config.ServiceName {
			return true
		}
		if containsComponent(containerName, p.config.ServiceName) {
			return true
		}
	}

	// Check k8s.container.name
	if val, ok := resourceAttrs.Get("k8s.container.name"); ok {
		containerName := val.Str()
		if containerName == p.config.ServiceName {
			return true
		}
		if containsComponent(containerName, p.config.ServiceName) {
			return true
		}
	}

	return false
}

// containsComponent checks if a container name contains a component.
func containsComponent(containerName, component string) bool {
	return len(containerName) > 0 && len(component) > 0 &&
		(containerName == component ||
			contains(containerName, "-"+component+"-") ||
			hasPrefix(containerName, component+"-") ||
			hasSuffix(containerName, "-"+component))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func indexOfChar(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func (p *klogProcessor) processLogRecord(lr plog.LogRecord) {
	// Get body as string
	var body string
	if lr.Body().Type() == pcommon.ValueTypeStr {
		body = lr.Body().Str()
	} else if lr.Body().Type() == pcommon.ValueTypeMap {
		bodyMap := lr.Body().Map()
		if textVal, ok := bodyMap.Get("text"); ok {
			body = textVal.Str()
		}
	}

	if body == "" {
		return
	}

	// Check if this looks like a klog entry
	if !IsKlog(body) {
		return
	}

	// Parse the log
	info := ParseKlog(body)
	if info == nil {
		return
	}

	// Save original message
	attrs := lr.Attributes()
	attrs.PutStr("original_message", body)

	// Set klog-specific fields as attributes
	if info.Level != "" {
		attrs.PutStr("log.level", info.Level)
	}
	if info.File != "" && info.Line != "" {
		attrs.PutStr("code.filepath", info.File+":"+info.Line)
	}
	if info.PID != "" {
		attrs.PutStr("process.pid", info.PID)
	}

	// Copy structured fields as attributes
	for k, v := range info.Extra {
		attrs.PutStr("log."+k, v)
	}

	// Extract K8s-specific fields from kubelet logs
	// pod="namespace/podname" -> k8s.namespace.name, k8s.pod.name
	if podVal, hasPod := info.Extra["pod"]; hasPod && podVal != "" {
		if idx := indexOfChar(podVal, '/'); idx > 0 && idx < len(podVal)-1 {
			attrs.PutStr("k8s.namespace.name", podVal[:idx])
			attrs.PutStr("k8s.pod.name", podVal[idx+1:])
		}
	}
	// podUID -> k8s.pod.uid
	if podUID, hasPodUID := info.Extra["podUID"]; hasPodUID && podUID != "" {
		attrs.PutStr("k8s.pod.uid", podUID)
	}
	// containerName -> k8s.container.name
	if containerName, hasContainer := info.Extra["containerName"]; hasContainer && containerName != "" {
		attrs.PutStr("k8s.container.name", containerName)
	}
	// containerID -> container.id
	if containerID, hasContainerID := info.Extra["containerID"]; hasContainerID && containerID != "" {
		attrs.PutStr("container.id", containerID)
	}
	// podID -> k8s.pod.uid (alternative field name)
	if podID, hasPodID := info.Extra["podID"]; hasPodID && podID != "" {
		if _, exists := attrs.Get("k8s.pod.uid"); !exists {
			attrs.PutStr("k8s.pod.uid", podID)
		}
	}

	// Set severity from level
	if p.config.AdjustSeverity && info.Level != "" {
		sevNum, sevText := SeverityFromKlogLevel(info.Level)
		lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
		lr.SetSeverityText(sevText)
	}

	// Rewrite body to the message
	if p.config.RewriteMessages && info.Message != "" {
		finalMessage := info.Message

		// Append key context fields to the message for better readability
		// err - error details
		if errVal, hasErr := info.Extra["err"]; hasErr && errVal != "" {
			finalMessage += ": " + errVal
		}
		// realServer - kube-proxy real server info
		if rsVal, hasRS := info.Extra["realServer"]; hasRS && rsVal != "" {
			finalMessage += " [" + rsVal + "]"
		}

		lr.Body().SetStr(finalMessage)
	}
}
