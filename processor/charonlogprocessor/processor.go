// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package charonlogprocessor

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type charonLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *charonLogProcessor {
	return &charonLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *charonLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *charonLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Charon log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
	)
	return nil
}

func (p *charonLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *charonLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
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

func (p *charonLogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		if serviceName == p.config.ServiceName {
			return true
		}
		// Also match if service name contains charon
		if strings.Contains(strings.ToLower(serviceName), "charon") {
			return true
		}
	}
	return false
}

func (p *charonLogProcessor) processLogRecord(lr plog.LogRecord) {
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

	// Check if this looks like a charon entry
	if !IsCharonLog(body) {
		return
	}

	// Parse the log
	info := ParseCharonLog(body)
	if info == nil {
		return
	}

	attrs := lr.Attributes()

	// Save original message only if not already set
	if _, exists := attrs.Get("original_message"); !exists {
		attrs.PutStr("original_message", body)
	}

	// Set IPsec/charon-specific fields as attributes
	attrs.PutStr("ipsec.thread_id", info.ThreadID)
	attrs.PutStr("ipsec.component", info.Component)
	if info.ConnectionID != "" {
		attrs.PutStr("ipsec.connection_id", info.ConnectionID)
	}
	if info.ConnectionSeq != "" {
		attrs.PutStr("ipsec.connection_seq", info.ConnectionSeq)
	}

	// Copy extra fields as attributes
	for k, v := range info.Extra {
		attrs.PutStr(k, v)
	}

	// Rewrite body to cleaner message
	if p.config.RewriteMessages && info.Message != "" {
		// Format: [Component] message
		cleanMsg := "[" + info.Component + "] " + info.Message
		lr.Body().SetStr(cleanMsg)
	}
}
