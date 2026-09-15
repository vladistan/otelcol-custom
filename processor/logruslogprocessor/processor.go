// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logruslogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type logrusLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *logrusLogProcessor {
	return &logrusLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *logrusLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logrusLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Logrus log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
		zap.Bool("parse_timestamp", p.config.ParseTimestamp),
	)
	return nil
}

func (p *logrusLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *logrusLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
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

func (p *logrusLogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
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

func (p *logrusLogProcessor) processLogRecord(lr plog.LogRecord) {
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

	// Try to parse as Logrus JSON, text format, or Zap console format
	var info *LogrusLogInfo
	if IsLogrusJSON(body) {
		info = ParseLogrusJSON(body)
	} else if IsLogrusText(body) {
		info = ParseLogrusText(body)
	} else if IsZapConsole(body) {
		info = ParseZapConsole(body)
	}

	if info == nil {
		return
	}

	// Save original message
	attrs := lr.Attributes()
	attrs.PutStr("original_message", body)

	// Set level as attribute
	if info.Level != "" {
		attrs.PutStr("log.level", info.Level)
	}

	// Set zap-style fields as attributes
	if info.Caller != "" {
		attrs.PutStr("code.filepath", info.Caller)
	}
	if info.Controller != "" {
		attrs.PutStr("k8s.controller", info.Controller)
	}
	if info.Error != "" {
		attrs.PutStr("error.message", info.Error)
	}
	if info.Stacktrace != "" {
		attrs.PutStr("error.stacktrace", info.Stacktrace)
	}

	// Copy extra fields as attributes
	for k, v := range info.Extra {
		attrs.PutStr("log."+k, v)
	}

	// Set severity from level
	if p.config.AdjustSeverity && info.Level != "" {
		sevNum, sevText := SeverityFromLevel(info.Level)
		lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
		lr.SetSeverityText(sevText)
	}

	// Update timestamp if configured
	if p.config.ParseTimestamp && !info.Timestamp.IsZero() {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Timestamp))
	}

	// Rewrite body to the message
	if p.config.RewriteMessages && info.Message != "" {
		finalMessage := info.Message
		// If there's an "error" field, append it to the message for better readability
		if info.Error != "" {
			finalMessage = info.Message + ": " + info.Error
		}
		lr.Body().SetStr(finalMessage)
	}
}
