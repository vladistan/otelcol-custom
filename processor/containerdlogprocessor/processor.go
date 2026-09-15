// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containerdlogprocessor

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type containerdLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *containerdLogProcessor {
	return &containerdLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *containerdLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *containerdLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Containerd log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *containerdLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *containerdLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resourceAttrs := rl.Resource().Attributes()

		// Check if this is from containerd service
		if !p.isContainerdService(resourceAttrs) {
			continue
		}

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

// isContainerdService checks if resource attributes indicate containerd service.
func (p *containerdLogProcessor) isContainerdService(resourceAttrs pcommon.Map) bool {
	if val, ok := resourceAttrs.Get("service.name"); ok {
		serviceName := val.Str()
		return serviceName == p.config.ServiceName ||
			serviceName == "containerd" ||
			strings.HasSuffix(serviceName, "/containerd")
	}
	return false
}

func (p *containerdLogProcessor) processLogRecord(lr plog.LogRecord, resourceAttrs pcommon.Map) {
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

	// Check if this looks like a containerd log
	if !IsContainerdLog(body) {
		return
	}

	// Parse the log
	info := ParseContainerdLog(body)
	if info == nil {
		return
	}

	attrs := lr.Attributes()

	// Save original message
	if _, exists := attrs.Get("original_message"); !exists {
		attrs.PutStr("original_message", body)
	}

	// Set containerd-specific attributes
	attrs.PutStr("containerd.level", info.Level)

	if info.ContainerID != "" {
		attrs.PutStr("container.id", info.ContainerID)
	}

	if info.Namespace != "" {
		attrs.PutStr("containerd.namespace", info.Namespace)
	}

	if info.EventType != "" {
		attrs.PutStr("containerd.event_type", info.EventType)
	}

	if info.Image != "" {
		attrs.PutStr("container.image.name", info.Image)
	}

	if info.Error != "" {
		attrs.PutStr("error.message", info.Error)
	}

	// Set K8s metadata from PodSandboxMetadata
	if info.PodName != "" {
		attrs.PutStr("k8s.pod.name", info.PodName)
	}
	if info.PodUID != "" {
		attrs.PutStr("k8s.pod.uid", info.PodUID)
	}
	if info.PodNamespace != "" {
		attrs.PutStr("k8s.namespace.name", info.PodNamespace)
	}
	if info.PodAttempt != "" {
		attrs.PutStr("k8s.sandbox.attempt", info.PodAttempt)
	}

	// Set K8s metadata from ContainerMetadata
	if info.ContainerName != "" {
		attrs.PutStr("k8s.container.name", info.ContainerName)
	}
	if info.ContainerAttempt != "" {
		attrs.PutStr("k8s.container.attempt", info.ContainerAttempt)
	}

	// Set message attribute
	attrs.PutStr("message", info.Message)

	// Adjust severity based on log level
	if p.config.AdjustSeverity {
		p.setSeverity(lr, info.Level)
	}

	// Rewrite body to cleaner message
	if p.config.RewriteMessages && info.CleanMsg != "" {
		lr.Body().SetStr(info.CleanMsg)
	}
}

// setSeverity sets OTEL severity based on logrus level.
func (p *containerdLogProcessor) setSeverity(lr plog.LogRecord, level string) {
	switch strings.ToLower(level) {
	case "trace":
		lr.SetSeverityNumber(plog.SeverityNumberTrace)
		lr.SetSeverityText("TRACE")
	case "debug":
		lr.SetSeverityNumber(plog.SeverityNumberDebug)
		lr.SetSeverityText("DEBUG")
	case "info":
		lr.SetSeverityNumber(plog.SeverityNumberInfo)
		lr.SetSeverityText("INFO")
	case "warning", "warn":
		lr.SetSeverityNumber(plog.SeverityNumberWarn)
		lr.SetSeverityText("WARN")
	case "error":
		lr.SetSeverityNumber(plog.SeverityNumberError)
		lr.SetSeverityText("ERROR")
	case "fatal", "panic":
		lr.SetSeverityNumber(plog.SeverityNumberFatal)
		lr.SetSeverityText("FATAL")
	}
}
