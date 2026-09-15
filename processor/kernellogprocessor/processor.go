// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kernellogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type kernelLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *kernelLogProcessor {
	return &kernelLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *kernelLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *kernelLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Kernel log processor started",
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *kernelLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *kernelLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		// Check if this resource is from kernel
		if !p.isKernelSource(resourceAttrs) {
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

// isKernelSource checks if the resource attributes indicate kernel logs.
func (p *kernelLogProcessor) isKernelSource(resourceAttrs pcommon.Map) bool {
	// Check for journald kernel: process.command == "[kernel]"
	if val, ok := resourceAttrs.Get("process.command"); ok {
		if val.Str() == "[kernel]" {
			return true
		}
	}

	// Check for syslog kernel: service.name == "kernel"
	if val, ok := resourceAttrs.Get("service.name"); ok {
		if val.Str() == "kernel" {
			return true
		}
	}

	return false
}

func (p *kernelLogProcessor) processLogRecord(lr plog.LogRecord) {
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

	// Check if this looks like a kernel entry
	if !IsKernelLog(body) {
		return
	}

	// Parse the log
	info := ParseKernelLog(body)
	if info == nil {
		return
	}

	attrs := lr.Attributes()

	// Save original message only if not already set
	if _, exists := attrs.Get("original_message"); !exists {
		attrs.PutStr("original_message", body)
	}

	// Set kernel-specific fields as attributes
	if info.Subsystem != "" {
		attrs.PutStr("kernel.subsystem", info.Subsystem)
	}
	if info.Device != "" {
		attrs.PutStr("kernel.device", info.Device)
	}
	if info.ErrorType != "" {
		attrs.PutStr("kernel.error_type", info.ErrorType)
	}

	// Copy extra fields as attributes
	for k, v := range info.Extra {
		attrs.PutStr(k, v)
	}

	// Set severity from detected error type
	if p.config.AdjustSeverity && info.Severity != "" {
		sevNum, sevText := SeverityFromKernelLog(info)
		lr.SetSeverityNumber(plog.SeverityNumber(sevNum))
		lr.SetSeverityText(sevText)
	}

	// Rewrite body to cleaner message
	if p.config.RewriteMessages && info.Message != "" {
		lr.Body().SetStr(info.Message)
	}
}
