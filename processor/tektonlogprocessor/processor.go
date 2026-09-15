// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tektonlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type tektonLogProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *tektonLogProcessor {
	return &tektonLogProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *tektonLogProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *tektonLogProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Tekton log processor started",
		zap.String("service_name", p.config.ServiceName),
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
	)
	return nil
}

func (p *tektonLogProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *tektonLogProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
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

func (p *tektonLogProcessor) matchesServiceName(resourceAttrs pcommon.Map) bool {
	if p.config.ServiceName == "" {
		return false
	}

	// Check service.name
	if val, ok := resourceAttrs.Get("service.name"); ok {
		if val.Str() == p.config.ServiceName {
			return true
		}
	}

	// Check container.name
	if val, ok := resourceAttrs.Get("container.name"); ok {
		containerName := val.Str()
		if containerName == p.config.ServiceName ||
			containerName == "tekton-pipelines-controller" ||
			containerName == "tekton-pipelines-webhook" {
			return true
		}
	}

	// Check k8s.container.name
	if val, ok := resourceAttrs.Get("k8s.container.name"); ok {
		containerName := val.Str()
		if containerName == p.config.ServiceName ||
			containerName == "tekton-pipelines-controller" ||
			containerName == "tekton-pipelines-webhook" {
			return true
		}
	}

	return false
}

func (p *tektonLogProcessor) processLogRecord(lr plog.LogRecord) {
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

	attrs := lr.Attributes()

	// Try to parse as Tekton Event with ObjectReference
	if IsTektonEvent(body) {
		info := ParseTektonEvent(body)
		if info != nil {
			p.setEventAttributes(attrs, info)
			if p.config.RewriteMessages && info.CleanMessage != "" {
				lr.Body().SetStr(info.CleanMessage)
			}
			return
		}
	}

	// Try to parse as PipelineRun/TaskRun status
	if IsTektonStatus(body) {
		info := ParseTektonStatus(body)
		if info != nil {
			p.setStatusAttributes(attrs, info)
			if p.config.RewriteMessages && info.CleanMessage != "" {
				lr.Body().SetStr(info.CleanMessage)
			}
			return
		}
	}
}

func (p *tektonLogProcessor) setEventAttributes(attrs pcommon.Map, info *TektonLogInfo) {
	// Event attributes
	if info.EventType != "" {
		attrs.PutStr("tekton.event.type", info.EventType)
	}
	if info.EventReason != "" {
		attrs.PutStr("tekton.event.reason", info.EventReason)
	}

	// ObjectReference attributes
	if info.ObjectKind != "" {
		attrs.PutStr("tekton.object.kind", info.ObjectKind)
	}
	if info.ObjectNamespace != "" {
		attrs.PutStr("tekton.object.namespace", info.ObjectNamespace)
	}
	if info.ObjectName != "" {
		attrs.PutStr("tekton.object.name", info.ObjectName)
	}
	if info.ObjectUID != "" {
		attrs.PutStr("tekton.object.uid", info.ObjectUID)
	}
	if info.ObjectAPIVersion != "" {
		attrs.PutStr("tekton.object.api_version", info.ObjectAPIVersion)
	}

	// Task statistics
	p.setTaskStats(attrs, info)
}

func (p *tektonLogProcessor) setStatusAttributes(attrs pcommon.Map, info *TektonLogInfo) {
	// Resource attributes
	if info.ResourceType != "" {
		attrs.PutStr("tekton.resource.type", info.ResourceType)
	}
	if info.ResourceName != "" {
		attrs.PutStr("tekton.resource.name", info.ResourceName)
	}
	if info.Status != "" {
		attrs.PutStr("tekton.status", info.Status)
	}
	if info.StatusReason != "" {
		attrs.PutStr("tekton.status.reason", info.StatusReason)
	}

	// Task statistics
	p.setTaskStats(attrs, info)
}

func (p *tektonLogProcessor) setTaskStats(attrs pcommon.Map, info *TektonLogInfo) {
	if info.TasksCompleted > 0 || info.TasksFailed > 0 || info.TasksCancelled > 0 || info.TasksSkipped > 0 {
		attrs.PutInt("tekton.tasks.completed", int64(info.TasksCompleted))
		attrs.PutInt("tekton.tasks.failed", int64(info.TasksFailed))
		attrs.PutInt("tekton.tasks.cancelled", int64(info.TasksCancelled))
		attrs.PutInt("tekton.tasks.skipped", int64(info.TasksSkipped))
	}
}
