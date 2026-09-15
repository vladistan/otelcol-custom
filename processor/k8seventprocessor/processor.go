// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8seventprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type k8sEventProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Logs
}

func newProcessor(logger *zap.Logger, config *Config, nextConsumer consumer.Logs) *k8sEventProcessor {
	return &k8sEventProcessor{
		logger:       logger,
		config:       config,
		nextConsumer: nextConsumer,
	}
}

func (p *k8sEventProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *k8sEventProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("Kubernetes Event processor started",
		zap.Bool("rewrite_messages", p.config.RewriteMessages),
		zap.Bool("adjust_severity", p.config.AdjustSeverity),
	)
	return nil
}

func (p *k8sEventProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *k8sEventProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)

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

func (p *k8sEventProcessor) processLogRecord(lr plog.LogRecord) {
	// Only process map bodies (structured K8s events)
	if lr.Body().Type() != pcommon.ValueTypeMap {
		return
	}

	bodyMap := lr.Body().Map()

	// Try multiple formats:
	// 1. body.structured.object (k8sobjectsreceiver watch format)
	// 2. body.object (alternative format)
	// 3. body itself contains kind=Event (direct event)

	var object pcommon.Map
	var found bool

	// Format 1: body.structured.object
	if structuredVal, ok := bodyMap.Get("structured"); ok && structuredVal.Type() == pcommon.ValueTypeMap {
		structured := structuredVal.Map()
		if objectVal, ok := structured.Get("object"); ok && objectVal.Type() == pcommon.ValueTypeMap {
			object = objectVal.Map()
			found = true
		}
	}

	// Format 2: body.object (direct)
	if !found {
		if objectVal, ok := bodyMap.Get("object"); ok && objectVal.Type() == pcommon.ValueTypeMap {
			object = objectVal.Map()
			found = true
		}
	}

	// Format 3: body itself is the event
	if !found {
		if kindVal, ok := bodyMap.Get("kind"); ok && kindVal.Str() == "Event" {
			object = bodyMap
			found = true
		}
	}

	if !found {
		return
	}

	// Get the kind of K8s object
	kindVal, ok := object.Get("kind")
	if !ok {
		return
	}

	kind := kindVal.Str()
	switch kind {
	case "Event":
		p.processEventObject(lr, object)
	case "Pod":
		p.processPodObject(lr, object)
	}
}

func (p *k8sEventProcessor) processEventObject(lr plog.LogRecord, object pcommon.Map) {
	// Extract event fields
	eventType := getNestedString(object, "type")
	reason := getNestedString(object, "reason")
	note := getNestedString(object, "note")

	// If note is empty, try "message" field (older format)
	if note == "" {
		note = getNestedString(object, "message")
	}

	// Get regarding object info
	var regardingKind, regardingName, regardingNamespace string
	if regardingVal, ok := object.Get("regarding"); ok && regardingVal.Type() == pcommon.ValueTypeMap {
		regarding := regardingVal.Map()
		regardingKind = getNestedString(regarding, "kind")
		regardingName = getNestedString(regarding, "name")
		regardingNamespace = getNestedString(regarding, "namespace")
	}

	// Try involvedObject if regarding is empty (older event format)
	if regardingKind == "" {
		if involvedVal, ok := object.Get("involvedObject"); ok && involvedVal.Type() == pcommon.ValueTypeMap {
			involved := involvedVal.Map()
			regardingKind = getNestedString(involved, "kind")
			regardingName = getNestedString(involved, "name")
			regardingNamespace = getNestedString(involved, "namespace")
		}
	}

	// Build human-readable message
	var message string
	if regardingKind != "" && regardingName != "" {
		if regardingNamespace != "" {
			message = fmt.Sprintf("%s %s/%s", regardingKind, regardingNamespace, regardingName)
		} else {
			message = fmt.Sprintf("%s %s", regardingKind, regardingName)
		}
	}

	if reason != "" {
		if message != "" {
			message += ": " + reason
		} else {
			message = reason
		}
	}

	if note != "" {
		if message != "" {
			message += " - " + note
		} else {
			message = note
		}
	}

	// Set attributes
	attrs := lr.Attributes()
	if eventType != "" {
		attrs.PutStr("k8s.event.type", eventType)
	}
	if reason != "" {
		attrs.PutStr("k8s.event.reason", reason)
	}
	if regardingKind != "" {
		attrs.PutStr("k8s.event.regarding.kind", regardingKind)
	}
	if regardingName != "" {
		attrs.PutStr("k8s.event.regarding.name", regardingName)
	}
	if regardingNamespace != "" {
		attrs.PutStr("k8s.event.regarding.namespace", regardingNamespace)
	}

	// Get source component
	if sourceVal, ok := object.Get("deprecatedSource"); ok && sourceVal.Type() == pcommon.ValueTypeMap {
		source := sourceVal.Map()
		if compVal, ok := source.Get("component"); ok {
			attrs.PutStr("k8s.event.source", compVal.Str())
		}
	}
	// Try "source" if deprecatedSource is not found
	if _, ok := attrs.Get("k8s.event.source"); !ok {
		if sourceVal, ok := object.Get("source"); ok && sourceVal.Type() == pcommon.ValueTypeMap {
			source := sourceVal.Map()
			if compVal, ok := source.Get("component"); ok {
				attrs.PutStr("k8s.event.source", compVal.Str())
			}
		}
	}

	// Set severity based on event type
	if p.config.AdjustSeverity {
		switch eventType {
		case "Normal":
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
		case "Warning":
			lr.SetSeverityNumber(plog.SeverityNumberWarn)
			lr.SetSeverityText("WARN")
		default:
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
		}
	}

	// Rewrite body to human-readable message
	if p.config.RewriteMessages && message != "" {
		lr.Body().SetStr(message)
	}
}

func (p *k8sEventProcessor) processPodObject(lr plog.LogRecord, object pcommon.Map) {
	attrs := lr.Attributes()

	var podName, podNamespace, nodeName, phase string
	var totalRestarts int64
	var readyCount, totalCount int

	// Extract metadata
	if metadataVal, ok := object.Get("metadata"); ok && metadataVal.Type() == pcommon.ValueTypeMap {
		metadata := metadataVal.Map()
		podName = getNestedString(metadata, "name")
		podNamespace = getNestedString(metadata, "namespace")
		if podName != "" {
			attrs.PutStr("k8s.pod.name", podName)
		}
		if podNamespace != "" {
			attrs.PutStr("k8s.pod.namespace", podNamespace)
		}
		if uid := getNestedString(metadata, "uid"); uid != "" {
			attrs.PutStr("k8s.pod.uid", uid)
		}
	}

	// Extract spec (for node name)
	if specVal, ok := object.Get("spec"); ok && specVal.Type() == pcommon.ValueTypeMap {
		spec := specVal.Map()
		nodeName = getNestedString(spec, "nodeName")
		if nodeName != "" {
			attrs.PutStr("k8s.node.name", nodeName)
		}
	}

	// Extract status
	if statusVal, ok := object.Get("status"); ok && statusVal.Type() == pcommon.ValueTypeMap {
		status := statusVal.Map()

		// Pod phase
		phase = getNestedString(status, "phase")
		if phase != "" {
			attrs.PutStr("k8s.pod.status.phase", phase)
		}

		// Container statuses - extract restart count and ready state
		if containerStatusesVal, ok := status.Get("containerStatuses"); ok && containerStatusesVal.Type() == pcommon.ValueTypeSlice {
			containerStatuses := containerStatusesVal.Slice()

			for i := 0; i < containerStatuses.Len(); i++ {
				cs := containerStatuses.At(i)
				if cs.Type() != pcommon.ValueTypeMap {
					continue
				}
				csMap := cs.Map()
				totalCount++

				// Get restart count
				if restartVal, ok := csMap.Get("restartCount"); ok {
					totalRestarts += restartVal.Int()
				}

				// Get ready state
				if readyVal, ok := csMap.Get("ready"); ok && readyVal.Bool() {
					readyCount++
				}
			}

			attrs.PutInt("k8s.pod.restarts", totalRestarts)
			attrs.PutStr("k8s.pod.ready", fmt.Sprintf("%d/%d", readyCount, totalCount))
		}
	}

	// Set severity based on phase
	if p.config.AdjustSeverity {
		switch phase {
		case "Running", "Succeeded":
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
		case "Pending":
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
		case "Failed":
			lr.SetSeverityNumber(plog.SeverityNumberError)
			lr.SetSeverityText("ERROR")
		default:
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
		}
	}

	// Build human-readable message for Pod
	// Example: "Pod opentelemetry/otel-main-0: Running (1/1 ready, 0 restarts) on k8s-4"
	if p.config.RewriteMessages && podName != "" {
		var message string
		if podNamespace != "" {
			message = fmt.Sprintf("Pod %s/%s", podNamespace, podName)
		} else {
			message = fmt.Sprintf("Pod %s", podName)
		}

		if phase != "" {
			message += ": " + phase
		}

		if totalCount > 0 {
			message += fmt.Sprintf(" (%d/%d ready", readyCount, totalCount)
			if totalRestarts > 0 {
				message += fmt.Sprintf(", %d restarts", totalRestarts)
			}
			message += ")"
		}

		if nodeName != "" {
			message += " on " + nodeName
		}

		lr.Body().SetStr(message)
	}
}

func getNestedString(m pcommon.Map, key string) string {
	if val, ok := m.Get(key); ok && val.Type() == pcommon.ValueTypeStr {
		return val.Str()
	}
	return ""
}
