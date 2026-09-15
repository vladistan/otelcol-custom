// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filterlogprocessor

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

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) Start(_ context.Context, _ component.Host) error {
	return nil
}

func (p *logsProcessor) Shutdown(_ context.Context) error {
	return nil
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

	// Get the source text - try body first (where transform/syslog puts the parsed content),
	// then fall back to source_attribute (original_message).
	// The body already has the CSV extracted by transform/syslog for filterlog entries.
	var sourceText string
	bodyText := lr.Body().AsString()
	if IsFilterLog(bodyText) {
		sourceText = bodyText
	} else if val, ok := attrs.Get(p.config.SourceAttribute); ok {
		sourceText = val.AsString()
	}

	if sourceText == "" {
		return
	}

	// Check if this looks like a filterlog entry
	if !IsFilterLog(sourceText) {
		return
	}

	// Parse the filterlog entry
	info := ParseFilterLog(sourceText)
	if info == nil {
		return
	}

	// Set service.name to filterlog for proper classification
	// Safe for syslog sources where each message is typically its own ResourceLog
	resourceAttrs.PutStr("service.name", "filterlog")

	// Set OTEL semantic convention attributes
	if info.Interface != "" {
		attrs.PutStr("network.interface.name", info.Interface)
	}
	if info.Protocol != "" {
		attrs.PutStr("network.transport", info.Protocol)
	}
	if info.IPVersion != "" {
		attrs.PutStr("network.type", NormalizeIPVersion(info.IPVersion))
	}
	if info.SourceIP != "" {
		attrs.PutStr("source.ip", info.SourceIP)
	}
	if info.SourcePort != "" {
		attrs.PutStr("source.port", info.SourcePort)
	}
	if info.DestinationIP != "" {
		attrs.PutStr("destination.ip", info.DestinationIP)
	}
	if info.DestinationPort != "" {
		attrs.PutStr("destination.port", info.DestinationPort)
	}
	if info.Action != "" {
		attrs.PutStr("event.action", info.Action)
	}
	if info.Length != "" {
		attrs.PutStr("network.bytes", info.Length)
	}

	// Set packet filter specific attributes (pf.* namespace)
	if info.RuleID != "" {
		attrs.PutStr("pf.rule.id", info.RuleID)
	}
	if info.RuleTracker != "" {
		attrs.PutStr("pf.rule.tracker", info.RuleTracker)
	}
	if info.Direction != "" {
		attrs.PutStr("pf.direction", NormalizeDirection(info.Direction))
	}
	if info.Reason != "" {
		attrs.PutStr("pf.reason", info.Reason)
	}
	if info.TTL != "" {
		attrs.PutStr("pf.ip.ttl", info.TTL)
	}
	if info.TCPFlags != "" {
		attrs.PutStr("pf.tcp.flags", info.TCPFlags)
	}
	if info.ICMPType != "" {
		attrs.PutStr("pf.icmp.type", info.ICMPType)
	}
	if info.ICMPID != "" {
		attrs.PutStr("pf.icmp.id", info.ICMPID)
	}

	// Rewrite the body to a clean message if configured
	if p.config.RewriteMessages && info.CleanMessage != "" {
		lr.Body().SetStr(info.CleanMessage)
	}
}
