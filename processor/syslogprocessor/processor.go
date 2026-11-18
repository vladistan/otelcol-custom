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

// ConsumeLogs splits records by the host.name and service.name they end with.
func (p *syslogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	return p.nextConsumer.ConsumeLogs(ctx, processPerResource(ld, p.processLogRecord))
}

func (p *syslogProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
	attrs := lr.Attributes()

	var message string
	var regexFailed bool

	messageVal, ok := attrs.Get(p.config.SourceAttribute)
	if ok {
		message = messageVal.Str()
	}

	// No source attribute: the regex parser failed, usually for want of a <priority> prefix.
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

	if regexFailed {
		attrs.PutBool("syslog.parse_error", true)
	}

	if p.config.PreserveOriginal {
		if _, exists := attrs.Get("original_message"); !exists {
			attrs.PutStr("original_message", message)
		}
	}

	info := Parse(message)
	if info == nil {
		p.mapSeverity(lr, attrs)
		attrs.PutStr("log.source", "syslog")
		return
	}

	if info.Format == FormatRFC3164 && IsEmbeddedRFC5424(info.Content) {
		embeddedInfo := Parse(info.Content)
		if embeddedInfo != nil {
			// The inner RFC5424 fields are more specific; an empty inner host keeps the outer one.
			if embeddedInfo.Appname != "" {
				info.Appname = embeddedInfo.Appname
			}
			if embeddedInfo.ProcID != "" {
				info.ProcID = embeddedInfo.ProcID
			}
			info.Content = embeddedInfo.Content
			if embeddedInfo.Hostname != "" {
				info.Hostname = embeddedInfo.Hostname
			}
		}
	}

	host := info.Hostname
	if host == "" && info.HostMissing {
		// The line has no HOSTNAME: the sender is the host the receiver saw. The stanza inputs write the
		// peer name on the record; a resource attribute of the same name is the fallback.
		if peer, ok := attrs.Get(peerKey); ok {
			host = peer.Str()
		} else if peer, ok := resourceAttrs.Get(peerKey); ok {
			host = peer.Str()
		}
	}
	if p.config.SetHostname {
		switch {
		case host != "":
			resourceAttrs.PutStr(hostKey, host)
		case info.HostMissing:
			// No host in the line and no peer name: the host is unset, not the host of the batch.
			resourceAttrs.Remove(hostKey)
		}
	}

	if p.config.SetServiceName && info.Appname != "" {
		resourceAttrs.PutStr(serviceKey, info.Appname)
	}

	if info.ProcID != "" {
		attrs.PutStr("process.pid", info.ProcID)
	}

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

	if netPeerIP, ok := attrs.Get("net.peer.ip"); ok {
		attrs.PutStr("source.ip", netPeerIP.Str())
	}

	p.mapSeverity(lr, attrs)

	if p.config.CleanBody && info.Content != "" {
		lr.Body().SetStr(info.Content)
	}

	attrs.PutStr("log.source", "syslog")
}

func (p *syslogProcessor) mapSeverity(lr plog.LogRecord, attrs pcommon.Map) {
	if !p.config.MapSeverity {
		return
	}
	priVal, ok := attrs.Get("priority")
	if !ok {
		return
	}
	if number, text, ok := MapPriorityToSeverity(priVal.Str()); ok {
		lr.SetSeverityNumber(number)
		lr.SetSeverityText(text)
	}
}
