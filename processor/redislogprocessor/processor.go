// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redislogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type logsProcessor struct {
	config       *Config
	logger       *zap.Logger
	nextConsumer consumer.Logs
}

func (p *logsProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Redis log processor started",
		zap.String("source_attribute", p.config.SourceAttribute),
		zap.Bool("set_service_name", p.config.SetServiceName),
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("preserve_original", p.config.PreserveOriginal),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *logsProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				p.processLogRecord(lr, resourceAttrs)
			}
		}
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

func (p *logsProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
	attrs := lr.Attributes()

	// Get the source text
	var sourceText string
	if p.config.SourceAttribute != "" {
		if val, ok := attrs.Get(p.config.SourceAttribute); ok {
			sourceText = val.Str()
		}
	}

	// Fall back to body if no source attribute or not found
	if sourceText == "" {
		if lr.Body().Type() == pcommon.ValueTypeStr {
			sourceText = lr.Body().Str()
		} else if lr.Body().Type() == pcommon.ValueTypeMap {
			// Handle journald JSON format: {"text": "..."}
			bodyMap := lr.Body().Map()
			if textVal, ok := bodyMap.Get("text"); ok {
				sourceText = textVal.Str()
			}
		}
	}

	if sourceText == "" {
		return
	}

	// Quick check before full parsing
	if !IsRedisLog(sourceText) {
		return
	}

	// Parse the log line
	info := ParseRedisLog(sourceText)
	if info == nil {
		return
	}

	// Preserve original message if configured
	if p.config.PreserveOriginal {
		attrs.PutStr("original_message", sourceText)
	}

	// Set service.name if configured and not already set
	if p.config.SetServiceName {
		if _, found := resourceAttrs.Get("service.name"); !found {
			resourceAttrs.PutStr("service.name", p.config.ServiceName)
		}
	}

	// Set severity based on Redis log level
	if p.config.AdjustSeverity {
		severityNum, severityText := SeverityFromLevel(info.LevelChar)
		lr.SetSeverityNumber(plog.SeverityNumber(severityNum))
		lr.SetSeverityText(severityText)
	}

	// Rename full_message to original_message (edge collector uses full_message)
	if fullMsg, ok := attrs.Get("full_message"); ok {
		attrs.PutStr("original_message", fullMsg.Str())
		attrs.Remove("full_message")
	}

	// Set Redis-specific attributes
	attrs.PutStr("redis.pid", info.PID)
	attrs.PutStr("redis.role", info.RoleName)
	attrs.PutStr("redis.level", info.LevelName)

	// Rewrite body to cleaned message
	if p.config.RewriteMessages {
		lr.Body().SetStr(BuildCleanedMessage(info))
	}
}
