// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type syslogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *syslogProcessor {
	return &syslogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *syslogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *syslogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Syslog processor started",
		zap.String("source_attribute", p.config.SourceAttribute),
		zap.Bool("set_hostname", p.config.SetHostname),
		zap.Bool("set_service_name", p.config.SetServiceName),
		zap.Bool("map_severity", p.config.MapSeverity),
	)
	return nil
}

func (p *syslogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *syslogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
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

func (p *syslogProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
	attrs := lr.Attributes()

	// Get the raw syslog message from the configured source attribute
	// If the attribute doesn't exist (regex parser failed), try to get from body
	var message string
	var regexFailed bool

	messageVal, ok := attrs.Get(p.config.SourceAttribute)
	if ok {
		message = messageVal.Str()
	}

	// If message is empty but body has content, the regex parser likely failed
	// This happens when syslog messages don't have the expected <priority> prefix
	if message == "" && lr.Body().Type() == pcommon.ValueTypeStr {
		bodyStr := lr.Body().Str()
		if bodyStr != "" {
			message = bodyStr
			regexFailed = true
		}
	}

	if message == "" {
		return
	}

	// Mark if regex parsing failed (no priority extracted)
	if regexFailed {
		attrs.PutBool("syslog.parse_error", true)
	}

	// Preserve original message if configured
	if p.config.PreserveOriginal {
		if _, exists := attrs.Get("original_message"); !exists {
			attrs.PutStr("original_message", message)
		}
	}

	// Parse the syslog message
	info := Parse(message)
	if info == nil {
		// Unable to parse, but still try to map severity from priority
		if p.config.MapSeverity {
			if priVal, ok := attrs.Get("priority"); ok {
				if sevNum, sevText := MapPriorityToSeverity(priVal.Str()); sevNum > 0 {
					lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
					lr.SetSeverityText(sevText)
				}
			}
		}
		// Set log source attribute
		attrs.PutStr("log.source", "syslog")
		return
	}

	// Check for embedded RFC5424 in RFC3164 (double-wrapped BSD syslog)
	if info.Format == FormatRFC3164 && IsEmbeddedRFC5424(info.Content) {
		embeddedInfo := Parse(info.Content)
		if embeddedInfo != nil {
			// Use the inner RFC5424 fields (they're more specific)
			if embeddedInfo.Appname != "" {
				info.Appname = embeddedInfo.Appname
			}
			if embeddedInfo.ProcID != "" {
				info.ProcID = embeddedInfo.ProcID
			}
			info.Content = embeddedInfo.Content
			// Keep outer hostname if inner is empty
			if embeddedInfo.Hostname != "" {
				info.Hostname = embeddedInfo.Hostname
			}
		}
	}

	// Set resource attributes
	if p.config.SetHostname && info.Hostname != "" {
		resourceAttrs.PutStr("host.name", info.Hostname)
	}

	if p.config.SetServiceName && info.Appname != "" {
		resourceAttrs.PutStr("service.name", info.Appname)
	}

	// Set log record attributes
	if info.ProcID != "" {
		attrs.PutStr("process.pid", info.ProcID)
	}

	// Handle Cisco-specific fields
	if info.Format == FormatCiscoIOS {
		if info.CiscoFacility != "" {
			attrs.PutStr("cisco.facility", info.CiscoFacility)
		}
		if info.CiscoMnemonic != "" {
			attrs.PutStr("cisco.mnemonic", info.CiscoMnemonic)
		}
		if info.CiscoSeq != "" {
			attrs.PutStr("cisco.sequence", info.CiscoSeq)
		}
	}

	// Capture source IP if available
	if netPeerIP, ok := attrs.Get("net.peer.ip"); ok {
		attrs.PutStr("source.ip", netPeerIP.Str())
	}

	// Map syslog priority to OTEL severity
	if p.config.MapSeverity {
		if priVal, ok := attrs.Get("priority"); ok {
			if sevNum, sevText := MapPriorityToSeverity(priVal.Str()); sevNum > 0 {
				lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
				lr.SetSeverityText(sevText)
			}
		}
	}

	// Set body to the actual message content
	if p.config.CleanBody && info.Content != "" {
		lr.Body().SetStr(info.Content)
	}

	// Set log source attribute
	attrs.PutStr("log.source", "syslog")
}
