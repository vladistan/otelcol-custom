// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cronlogprocessor

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type cronLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *cronLogProcessor {
	return &cronLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *cronLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *cronLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Cron log processor started",
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
	)
	return nil
}

func (p *cronLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *cronLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		// Check if this resource is from cron based on service.name
		isCronByService := p.isCronService(resourceAttrs)

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				// Process if service indicates cron OR if message looks like cron
				// (syslog may have incorrect service.name but body contains cron message)
				if isCronByService || p.looksLikeCronLog(lr) {
					p.processLogRecord(lr, resourceAttrs)
				}
			}
		}
	}

	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

// looksLikeCronLog checks if the log record body looks like a cron log entry.
func (p *cronLogProcessor) looksLikeCronLog(lr plog.LogRecord) bool {
	var body string
	if lr.Body().Type() == pcommon.ValueTypeStr {
		body = lr.Body().Str()
	} else if lr.Body().Type() == pcommon.ValueTypeMap {
		bodyMap := lr.Body().Map()
		if textVal, ok := bodyMap.Get("text"); ok {
			body = textVal.Str()
		}
	}
	return IsCronLog(body)
}

// isCronService checks if the resource attributes indicate a cron service.
func (p *cronLogProcessor) isCronService(resourceAttrs pcommon.Map) bool {
	// Check service.name
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		// Match: cron.service, crond.service, /usr/sbin/cron
		if serviceName == "cron.service" ||
			serviceName == "crond.service" ||
			serviceName == "/usr/sbin/cron" ||
			strings.HasSuffix(serviceName, "/cron") {
			return true
		}
	}

	// Check process.command
	if val, ok := resourceAttrs.Get("process.command"); ok {
		cmd := val.Str()
		if cmd == "cron" || cmd == "crond" ||
			strings.HasSuffix(cmd, "/cron") || strings.HasSuffix(cmd, "/crond") {
			return true
		}
	}

	return false
}

func (p *cronLogProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
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

	// Check if this looks like a cron entry
	if !IsCronLog(body) {
		return
	}

	// Parse the log
	info := ParseCronLog(body)
	if info == nil {
		return
	}

	attrs := lr.Attributes()

	// Save original message only if not already set
	if _, exists := attrs.Get("original_message"); !exists {
		attrs.PutStr("original_message", body)
	}

	// Set cron-specific fields as attributes
	attrs.PutStr("cron.type", info.Type)
	if info.User != "" {
		attrs.PutStr("cron.user", info.User)
	}
	if info.Command != "" {
		attrs.PutStr("cron.command", info.Command)
	}

	// Set user-friendly message attribute
	if info.Message != "" {
		attrs.PutStr("message", info.Message)
	}

	// Note: We no longer set service.name here because resource attributes
	// are shared across all logs in a ResourceLog batch. Setting service.name
	// would affect non-cron logs in the same batch. The syslog processor
	// already sets service.name correctly from the appname (e.g., /usr/sbin/cron).
	// The cron.type attribute can be used to identify cron logs.

	// Rewrite body to cleaner message
	if p.config.RewriteMessages && info.Message != "" {
		lr.Body().SetStr(info.Message)
	}
}
