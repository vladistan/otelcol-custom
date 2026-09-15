// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

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
	p.logger.Info("Nginx log processor started",
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
	if !IsNginxLog(sourceText) {
		return
	}

	// Parse the log line
	info := ParseNginxLog(sourceText)
	if info == nil {
		return
	}

	// Set service.name if configured and not already set
	if p.config.SetServiceName {
		if _, found := resourceAttrs.Get("service.name"); !found {
			resourceAttrs.PutStr("service.name", p.config.ServiceName)
		}
	}

	// Set client address
	if info.ClientAddr != "" {
		attrs.PutStr("client.address", info.ClientAddr)
	}

	// Set HTTP request fields
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

	// Set HTTP response fields
	if info.StatusCode > 0 {
		attrs.PutInt("http.response.status_code", int64(info.StatusCode))
	}
	if info.BodyBytes >= 0 {
		attrs.PutInt("http.response.body.bytes", info.BodyBytes)
	}

	// Set referer if present
	if info.Referer != "" {
		attrs.PutStr("http.request.header.referer", info.Referer)
	}

	// Set user agent - original and parsed fields
	if info.UserAgent != "" {
		attrs.PutStr("user_agent.original", info.UserAgent)

		// Parse user agent for structured fields
		if uaInfo := ParseUserAgent(info.UserAgent); uaInfo != nil {
			if uaInfo.Name != "" {
				attrs.PutStr("user_agent.name", uaInfo.Name)
			}
			if uaInfo.Version != "" {
				attrs.PutStr("user_agent.version", uaInfo.Version)
			}
			if uaInfo.OSName != "" {
				attrs.PutStr("os.name", uaInfo.OSName)
			}
			if uaInfo.Device != "" {
				attrs.PutStr("device.type", uaInfo.Device)
			}
		}
	}

	// Set X-Forwarded-For if present
	if info.ForwardedFor != "" {
		attrs.PutStr("network.forwarded_for", info.ForwardedFor)
	}

	// Set remote user if present
	if info.RemoteUser != "" {
		attrs.PutStr("enduser.id", info.RemoteUser)
	}

	// Set severity based on status code
	if info.StatusCode >= 500 {
		lr.SetSeverityNumber(plog.SeverityNumberError)
		lr.SetSeverityText("ERROR")
	} else if info.StatusCode >= 400 {
		lr.SetSeverityNumber(plog.SeverityNumberWarn)
		lr.SetSeverityText("WARN")
	} else {
		lr.SetSeverityNumber(plog.SeverityNumberInfo)
		lr.SetSeverityText("INFO")
	}

	// Update timestamp if configured
	if p.config.ParseTimestamp && !info.Timestamp.IsZero() {
		lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Timestamp))
	}

	// Always preserve original message
	attrs.PutStr("original_message", sourceText)

	// Set body to "{status_code} {method} {path}"
	newBody := fmt.Sprintf("%d %s %s", info.StatusCode, info.Method, info.Path)
	if info.Query != "" {
		newBody += "?" + info.Query
	}
	lr.Body().SetStr(newBody)
}
