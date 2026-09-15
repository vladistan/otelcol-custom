// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snubalogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type snubaLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *snubaLogProcessor {
	return &snubaLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *snubaLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *snubaLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Snuba log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("extract_target", p.config.ExtractTarget),
		zap.Bool("extract_storage", p.config.ExtractStorage),
	)
	return nil
}

func (p *snubaLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *snubaLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		// Check if this resource matches our service name
		if !p.matchesServiceName(resourceAttrs) {
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

func (p *snubaLogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
	// First check service.name
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		// Match exact name or container name containing the service
		if serviceName == p.config.ServiceName {
			return true
		}
		// Also match container names like "sentry-self-hosted-snuba-errors-consumer-1"
		if containsComponent(serviceName, p.config.ServiceName) {
			return true
		}
	}

	// Also check container.name for Docker containers
	// Docker logs from journald have service.name=docker.service and container info in resource attributes
	if val, ok := resourceAttrs.Get("container.name"); ok {
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
// e.g., "sentry-self-hosted-snuba-errors-consumer-1" contains "snuba"
func containsComponent(containerName, component string) bool {
	// Simple substring check for now
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

func (p *snubaLogProcessor) processLogRecord(lr plog.LogRecord) {
	// Get body as string - multiple formats possible
	var body string
	if lr.Body().Type() == pcommon.ValueTypeStr {
		body = lr.Body().Str()
	} else if lr.Body().Type() == pcommon.ValueTypeMap {
		// Handle journald JSON format: {"text": "..."}
		bodyMap := lr.Body().Map()
		if textVal, ok := bodyMap.Get("text"); ok {
			body = textVal.Str()
		}
	}

	// If body is empty, try getting from full_message attribute
	// Edge collector sometimes puts original log there
	if body == "" {
		attrs := lr.Attributes()
		if fullMsg, ok := attrs.Get("full_message"); ok {
			body = fullMsg.Str()
		}
	}

	if body == "" {
		return
	}

	// Check if this looks like a Snuba log
	if !IsSnubaLog(body) {
		return
	}

	// Parse the log
	info := ParseSnubaLog(body)
	if info == nil {
		return
	}

	// Save original message
	attrs := lr.Attributes()
	attrs.PutStr("original_message", body)

	// Rename full_message to original_message (edge collector uses full_message)
	if fullMsg, ok := attrs.Get("full_message"); ok {
		attrs.PutStr("original_message", fullMsg.Str())
		attrs.Remove("full_message")
	}

	// Set Snuba-specific attributes
	if p.config.ExtractTarget && info.Target != "" {
		attrs.PutStr("snuba.target", info.Target)
	}

	if p.config.ExtractStorage && info.Storage != "" {
		attrs.PutStr("snuba.storage", info.Storage)
	}

	// Set log format indicator
	if info.IsJSON {
		attrs.PutStr("snuba.format", "rust_tracing")
	} else {
		attrs.PutStr("snuba.format", "python")
	}

	// Set severity from parsed level
	if info.Level != "" {
		sevNum, sevText := SeverityFromLevel(info.Level)
		lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
		lr.SetSeverityText(sevText)
	}

	// Rewrite body to cleaned message
	if p.config.RewriteMessages && info.Message != "" {
		lr.Body().SetStr(BuildCleanedMessage(info))
	}

	// Extract additional fields from JSON if present
	if info.RawFields != nil {
		for k, v := range info.RawFields {
			// Skip message and storage (already handled)
			if k == "message" || k == "storage" {
				continue
			}
			// Add other fields as attributes
			switch val := v.(type) {
			case string:
				attrs.PutStr("snuba.field."+k, val)
			case float64:
				attrs.PutDouble("snuba.field."+k, val)
			case bool:
				attrs.PutBool("snuba.field."+k, val)
			}
		}
	}
}
