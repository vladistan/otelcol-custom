// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type uvicornLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *uvicornLogProcessor {
	return &uvicornLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *uvicornLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *uvicornLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Uvicorn log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("extract_http_fields", p.config.ExtractHTTPFields),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *uvicornLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *uvicornLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
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

func (p *uvicornLogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
	// First check service.name
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		if serviceName == p.config.ServiceName {
			return true
		}
		if containsComponent(serviceName, p.config.ServiceName) {
			return true
		}
	}

	// Also check container.name for Docker/K8s containers
	if val, ok := resourceAttrs.Get("container.name"); ok {
		containerName := val.Str()
		if containerName == p.config.ServiceName {
			return true
		}
		if containsComponent(containerName, p.config.ServiceName) {
			return true
		}
	}

	// Also check k8s.container.name
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

func (p *uvicornLogProcessor) processLogRecord(lr plog.LogRecord) {
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

	// Try full_message attribute if body is empty
	if body == "" {
		attrs := lr.Attributes()
		if fullMsg, ok := attrs.Get("full_message"); ok {
			body = fullMsg.Str()
		}
	}

	if body == "" {
		return
	}

	// Check if this looks like a Uvicorn log
	if !IsUvicornLog(body) {
		return
	}

	// Parse the log
	info := ParseUvicornLog(body)
	if info == nil {
		return
	}

	// Save original message
	attrs := lr.Attributes()
	attrs.PutStr("original_message", body)

	// Rename full_message to original_message
	if _, ok := attrs.Get("full_message"); ok {
		attrs.Remove("full_message")
	}

	// Extract HTTP fields if configured
	if p.config.ExtractHTTPFields {
		if info.ClientIP != "" {
			attrs.PutStr("client.ip", info.ClientIP)
			attrs.PutStr("client.address", info.ClientAddr)
		}
		if info.ClientPort != "" {
			attrs.PutStr("client.port", info.ClientPort)
		}
		if info.Method != "" {
			attrs.PutStr("http.request.method", info.Method)
		}
		if info.Path != "" {
			attrs.PutStr("url.path", info.Path)
		}
		if info.Query != "" {
			attrs.PutStr("url.query", info.Query)
		}
		if info.Protocol != "" {
			attrs.PutStr("network.protocol.name", "http")
			// Extract version from HTTP/1.1 -> 1.1
			if len(info.Protocol) > 5 {
				attrs.PutStr("network.protocol.version", info.Protocol[5:])
			}
		}
		if info.StatusCode > 0 {
			attrs.PutInt("http.response.status_code", int64(info.StatusCode))
		}
		if info.StatusText != "" {
			attrs.PutStr("http.response.status_text", info.StatusText)
		}
	}

	// Set severity from log level or status code
	if p.config.AdjustSeverity {
		// Prefer status code for severity (more meaningful for access logs)
		sevNum, sevText := SeverityFromStatusCode(info.StatusCode)
		lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
		lr.SetSeverityText(sevText)
	}

	// Rewrite body to cleaned message
	if p.config.RewriteMessages {
		lr.Body().SetStr(BuildCleanedMessage(info))
	}
}
