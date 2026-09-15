// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pgbouncerlogprocessor

import (
	"context"
	"fmt"

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
	p.logger.Info("PgBouncer log processor started",
		zap.String("source_attribute", p.config.SourceAttribute),
		zap.Bool("set_service_name", p.config.SetServiceName),
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("parse_timestamp", p.config.ParseTimestamp),
		zap.Bool("preserve_original", p.config.PreserveOriginal),
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
	if !IsPgBouncerLog(sourceText) {
		return
	}

	// Parse the log line
	info := ParsePgBouncerLog(sourceText)
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

	// Set severity based on PgBouncer log level
	severityNum, severityText := SeverityFromLevel(info.Level)
	lr.SetSeverityNumber(plog.SeverityNumber(severityNum))
	lr.SetSeverityText(severityText)

	// Set timestamp if configured and parsed
	if p.config.ParseTimestamp && !info.Timestamp.IsZero() {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Timestamp))
	}

	// Set PID
	if info.PID > 0 {
		attrs.PutInt("process.pid", int64(info.PID))
	}

	// Set PgBouncer-specific attributes
	if info.ConnectionID != "" {
		attrs.PutStr("pgbouncer.connection_id", info.ConnectionID)
	}

	// Set database info
	if info.Database != "" {
		attrs.PutStr("db.name", info.Database)
	}
	if info.User != "" {
		attrs.PutStr("db.user", info.User)
	}

	// Set client connection info
	if info.ClientIP != "" {
		attrs.PutStr("client.address", info.ClientIP)
	}
	if info.ClientPort > 0 {
		attrs.PutInt("client.port", int64(info.ClientPort))
	}

	// Set PgBouncer log level as attribute (original level before mapping)
	attrs.PutStr("pgbouncer.level", info.Level)

	// Set body to cleaned message
	newBody := info.Message
	if info.Database != "" && info.User != "" {
		// Include db/user context in shortened form
		newBody = fmt.Sprintf("[%s/%s] %s", info.Database, info.User, info.Message)
	}
	lr.Body().SetStr(newBody)
}
