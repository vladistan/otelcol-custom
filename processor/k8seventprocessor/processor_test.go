// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8seventprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

func config(mutate func(*Config)) *Config {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	if mutate != nil {
		mutate(cfg)
	}
	return cfg
}

func recordWith(t *testing.T, body map[string]any) plog.Logs {
	t.Helper()
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	if err := lr.Body().SetEmptyMap().FromRaw(body); err != nil {
		t.Fatalf("set body: %v", err)
	}
	return ld
}

func process(t *testing.T, cfg *Config, ld plog.Logs) plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if sink.LogRecordCount() != 1 {
		t.Fatalf("sink saw %d records, want 1", sink.LogRecordCount())
	}
	return sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func attr(t *testing.T, lr plog.LogRecord, key string) pcommon.Value {
	t.Helper()
	v, ok := lr.Attributes().Get(key)
	if !ok {
		t.Fatalf("attribute %q missing; have %v", key, lr.Attributes().AsRaw())
	}
	return v
}

func absent(t *testing.T, lr plog.LogRecord, key string) {
	t.Helper()
	if _, ok := lr.Attributes().Get(key); ok {
		t.Errorf("attribute %q present, want absent", key)
	}
}

func eventObject(eventType string) map[string]any {
	return map[string]any{
		"kind":   "Event",
		"type":   eventType,
		"reason": "BackOff",
		"note":   "Back-off restarting failed container",
		"regarding": map[string]any{
			"kind": "Pod", "name": "web-1", "namespace": "shop",
		},
		"deprecatedSource": map[string]any{"component": "kubelet"},
		"metadata":         map[string]any{"name": "web-1.18e1", "namespace": "shop"},
	}
}

func podObject(phase string) map[string]any {
	return map[string]any{
		"kind":     "Pod",
		"metadata": map[string]any{"name": "web-1", "namespace": "shop", "uid": "uid-1"},
		"spec":     map[string]any{"nodeName": "node-a"},
		"status": map[string]any{
			"phase": phase,
			"containerStatuses": []any{
				map[string]any{"restartCount": 2, "ready": true},
				map[string]any{"restartCount": 1, "ready": false},
			},
		},
	}
}

func TestEvent_AllThreeWatchFormats_ProduceSameResult(t *testing.T) {
	formats := map[string]map[string]any{
		"body.object":            {"type": "ADDED", "object": eventObject("Warning")},
		"body.structured.object": {"structured": map[string]any{"type": "ADDED", "object": eventObject("Warning")}},
		"body is the event":      eventObject("Warning"),
	}
	for name, body := range formats {
		t.Run(name, func(t *testing.T) {
			lr := process(t, config(nil), recordWith(t, body))

			if got, want := lr.Body().Str(), "Pod shop/web-1: BackOff - Back-off restarting failed container"; got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
				t.Errorf("severity = %v/%q, want WARN", lr.SeverityNumber(), lr.SeverityText())
			}
			want := map[string]string{
				"k8s.event.type":                "Warning",
				"k8s.event.reason":              "BackOff",
				"k8s.event.regarding.kind":      "Pod",
				"k8s.event.regarding.name":      "web-1",
				"k8s.event.regarding.namespace": "shop",
				"k8s.event.source":              "kubelet",
			}
			for key, value := range want {
				if got := attr(t, lr, key).Str(); got != value {
					t.Errorf("%s = %q, want %q", key, got, value)
				}
			}
		})
	}
}

func TestEvent_Normal_IsInfo_UnknownTypeIsInfo(t *testing.T) {
	for _, eventType := range []string{"Normal", "Other", ""} {
		lr := process(t, config(nil), recordWith(t, map[string]any{"object": eventObject(eventType)}))

		if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
			t.Errorf("type %q: severity = %v/%q, want INFO", eventType, lr.SeverityNumber(), lr.SeverityText())
		}
	}
}

func TestEvent_OlderFormat_UsesInvolvedObjectMessageAndSource(t *testing.T) {
	body := map[string]any{
		"kind":           "Event",
		"type":           "Normal",
		"reason":         "Started",
		"message":        "Started container web",
		"involvedObject": map[string]any{"kind": "Pod", "name": "web-1", "namespace": "shop"},
		"source":         map[string]any{"component": "kubelet"},
	}

	lr := process(t, config(nil), recordWith(t, body))

	if got, want := lr.Body().Str(), "Pod shop/web-1: Started - Started container web"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attr(t, lr, "k8s.event.source").Str(); got != "kubelet" {
		t.Errorf("k8s.event.source = %q, want kubelet", got)
	}
}

func TestEvent_RegardingWithoutNamespace_BodyOmitsNamespace(t *testing.T) {
	obj := map[string]any{
		"kind": "Event", "type": "Normal", "reason": "Created",
		"regarding": map[string]any{"kind": "Node", "name": "node-a"},
	}

	lr := process(t, config(nil), recordWith(t, map[string]any{"object": obj}))

	if got, want := lr.Body().Str(), "Node node-a: Created"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestEvent_OnlyNote_BodyIsNote_OnlyReason_BodyIsReason(t *testing.T) {
	onlyNote := map[string]any{"kind": "Event", "type": "Normal", "note": "just a note"}
	onlyReason := map[string]any{"kind": "Event", "type": "Normal", "reason": "Pulled"}

	if got := process(t, config(nil), recordWith(t, map[string]any{"object": onlyNote})).Body().Str(); got != "just a note" {
		t.Errorf("note-only body = %q", got)
	}
	if got := process(t, config(nil), recordWith(t, map[string]any{"object": onlyReason})).Body().Str(); got != "Pulled" {
		t.Errorf("reason-only body = %q", got)
	}
}

func TestEvent_EmptyMessage_BodyUnchanged(t *testing.T) {
	obj := map[string]any{"kind": "Event", "type": "Normal"}
	ld := recordWith(t, map[string]any{"object": obj})

	lr := process(t, config(nil), ld)

	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Errorf("body type = %v, want the original map", lr.Body().Type())
	}
}

func TestSwitchesOff_BodyAndSeverityUntouched(t *testing.T) {
	cfg := config(func(c *Config) { c.RewriteMessages = false; c.AdjustSeverity = false })

	lr := process(t, cfg, recordWith(t, map[string]any{"object": eventObject("Warning")}))

	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Errorf("body rewritten with rewrite_messages off")
	}
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("severity set with adjust_severity off: %v", lr.SeverityNumber())
	}
	if got := attr(t, lr, "k8s.event.reason").Str(); got != "BackOff" {
		t.Errorf("attributes should still be set; reason = %q", got)
	}
}

func TestPod_BodyAttributesAndSeverityPerPhase(t *testing.T) {
	cases := []struct {
		phase  string
		number plog.SeverityNumber
		text   string
	}{
		{"Running", plog.SeverityNumberInfo, "INFO"},
		{"Succeeded", plog.SeverityNumberInfo, "INFO"},
		{"Pending", plog.SeverityNumberInfo, "INFO"},
		{"Failed", plog.SeverityNumberError, "ERROR"},
		{"Unknown", plog.SeverityNumberInfo, "INFO"},
	}
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			lr := process(t, config(nil), recordWith(t, map[string]any{"object": podObject(tc.phase)}))

			if lr.SeverityNumber() != tc.number || lr.SeverityText() != tc.text {
				t.Errorf("severity = %v/%q, want %v/%q", lr.SeverityNumber(), lr.SeverityText(), tc.number, tc.text)
			}
			want := "Pod shop/web-1: " + tc.phase + " (1/2 ready, 3 restarts) on node-a"
			if lr.Body().Str() != want {
				t.Errorf("body = %q, want %q", lr.Body().Str(), want)
			}
		})
	}
}

func TestPod_Attributes(t *testing.T) {
	lr := process(t, config(nil), recordWith(t, map[string]any{"object": podObject("Running")}))

	strs := map[string]string{
		"k8s.pod.name": "web-1", "k8s.pod.namespace": "shop", "k8s.pod.uid": "uid-1",
		"k8s.node.name": "node-a", "k8s.pod.status.phase": "Running", "k8s.pod.ready": "1/2",
	}
	for key, value := range strs {
		if got := attr(t, lr, key).Str(); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if got := attr(t, lr, "k8s.pod.restarts").Int(); got != 3 {
		t.Errorf("k8s.pod.restarts = %d, want 3", got)
	}
}

func TestPod_NoContainerStatuses_NoRestartsOrReady(t *testing.T) {
	obj := map[string]any{
		"kind":     "Pod",
		"metadata": map[string]any{"name": "web-1"},
		"status":   map[string]any{"phase": "Pending"},
	}

	lr := process(t, config(nil), recordWith(t, map[string]any{"object": obj}))

	if got, want := lr.Body().Str(), "Pod web-1: Pending"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	absent(t, lr, "k8s.pod.restarts")
	absent(t, lr, "k8s.pod.ready")
}

func TestPod_ZeroRestarts_BodyOmitsRestarts(t *testing.T) {
	obj := podObject("Running")
	obj["status"] = map[string]any{
		"phase":             "Running",
		"containerStatuses": []any{map[string]any{"restartCount": 0, "ready": true}},
	}

	lr := process(t, config(nil), recordWith(t, map[string]any{"object": obj}))

	if got, want := lr.Body().Str(), "Pod shop/web-1: Running (1/1 ready) on node-a"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestPod_NonMapContainerStatus_IsSkipped(t *testing.T) {
	obj := podObject("Running")
	obj["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{"junk"}}

	lr := process(t, config(nil), recordWith(t, map[string]any{"object": obj}))

	if got := attr(t, lr, "k8s.pod.ready").Str(); got != "0/0" {
		t.Errorf("k8s.pod.ready = %q, want 0/0", got)
	}
}

func TestUnhandled_PassesUnchanged(t *testing.T) {
	cases := map[string]plog.Logs{
		"other kind":     recordWith(t, map[string]any{"object": map[string]any{"kind": "Deployment"}}),
		"no kind":        recordWith(t, map[string]any{"object": map[string]any{"x": "y"}}),
		"no object":      recordWith(t, map[string]any{"foo": "bar"}),
		"event not kind": recordWith(t, map[string]any{"kind": "Service"}),
	}
	for name, ld := range cases {
		t.Run(name, func(t *testing.T) {
			before := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Map().Len()

			lr := process(t, config(func(c *Config) { c.OTTLParity = true }), ld)

			if lr.Attributes().Len() != 0 || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
				t.Errorf("record changed: attrs=%v severity=%v", lr.Attributes().AsRaw(), lr.SeverityNumber())
			}
			if lr.Body().Type() != pcommon.ValueTypeMap || lr.Body().Map().Len() != before {
				t.Errorf("body changed: %v", lr.Body().AsRaw())
			}
		})
	}
}

func TestStringBody_PassesUnchanged(t *testing.T) {
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("plain")

	lr := process(t, config(func(c *Config) { c.OTTLParity = true }), ld)

	if lr.Body().Str() != "plain" || lr.Attributes().Len() != 0 {
		t.Errorf("string record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
}

func TestParityOff_SetsNoOTTLFields(t *testing.T) {
	lr := process(t, config(nil), recordWith(t, map[string]any{"type": "ADDED", "object": podObject("Running")}))

	for _, key := range []string{"k8s.object.kind", "k8s.object.action", "message_type", "message", "k8s.object.full", "k8s.pod.phase"} {
		absent(t, lr, key)
	}
}

func TestParity_Pod_SetsEveryOTTLField(t *testing.T) {
	obj := podObject("Failed")
	obj["metadata"] = map[string]any{
		"name": "web-1", "namespace": "shop", "uid": "uid-1",
		"labels": map[string]any{"app": "web", "environment": "prod", "job-name": "web-29"},
	}
	obj["status"] = map[string]any{
		"phase": "Failed",
		"containerStatuses": []any{
			map[string]any{
				"restartCount": 2, "ready": false,
				"state": map[string]any{"terminated": map[string]any{"exitCode": 137}},
			},
		},
	}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "MODIFIED", "object": obj}))

	strs := map[string]string{
		"k8s.object.kind": "Pod", "k8s.object.action": "MODIFIED",
		"k8s.namespace": "shop", "k8s.name": "web-1", "k8s.pod.phase": "Failed",
		"k8s.container.state": "terminated", "message_type": "k8sobject",
		"k8s.labels.app": "web", "k8s.labels.environment": "prod", "k8s.labels.job_name": "web-29",
		"message": "K8S MODIFIED Pod shop / web-1 Phase:Failed",
	}
	for key, value := range strs {
		if got := attr(t, lr, key).Str(); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if got := attr(t, lr, "k8s.container.exit_code").Int(); got != 137 {
		t.Errorf("k8s.container.exit_code = %d, want 137", got)
	}
	if got, want := lr.Body().Str(), "K8S MODIFIED Pod shop / web-1 Phase:Failed"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if v := attr(t, lr, "k8s.object.full"); v.Type() != pcommon.ValueTypeMap || v.Map().Len() != len(obj) {
		t.Errorf("k8s.object.full = %v", v.AsRaw())
	}
}

func TestParity_PodSeverityIsInfo_EvenWhenFailed(t *testing.T) {
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "MODIFIED", "object": podObject("Failed")}))

	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Errorf("severity = %v/%q, want INFO as the OTTL block sets it", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestParity_ContainerRunning_SetsStateAndNoExitCode(t *testing.T) {
	obj := podObject("Running")
	obj["status"] = map[string]any{
		"phase": "Running",
		"containerStatuses": []any{
			map[string]any{"state": map[string]any{"running": map[string]any{"startedAt": "t"}}},
		},
	}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": obj}))

	if got := attr(t, lr, "k8s.container.state").Str(); got != "running" {
		t.Errorf("k8s.container.state = %q, want running", got)
	}
	absent(t, lr, "k8s.container.exit_code")
}

func TestParity_PodWithoutContainerStatuses_SetsNoContainerFields(t *testing.T) {
	obj := map[string]any{
		"kind": "Pod", "metadata": map[string]any{"name": "web-1", "namespace": "shop"},
		"status": map[string]any{"phase": "Pending"},
	}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": obj}))

	absent(t, lr, "k8s.container.state")
	absent(t, lr, "k8s.container.exit_code")
	absent(t, lr, "k8s.labels.app")
}

func TestParity_Event_SetsEveryOTTLField(t *testing.T) {
	obj := eventObject("Warning")
	obj["deprecatedCount"] = 4
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": obj}))

	strs := map[string]string{
		"k8s.object.kind": "Event", "k8s.object.action": "ADDED",
		"k8s.namespace": "shop", "k8s.name": "web-1.18e1",
		"k8s.event.type": "Warning", "k8s.event.reason": "BackOff",
		"k8s.event.note":           "Back-off restarting failed container",
		"k8s.event.regarding.kind": "Pod", "k8s.event.regarding.name": "web-1",
		"k8s.event.regarding.namespace": "shop",
		"message_type":                  "k8sobject",
		"message":                       "K8S ADDED Event shop / web-1.18e1 BackOff",
	}
	for key, value := range strs {
		if got := attr(t, lr, key).Str(); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if v := attr(t, lr, "k8s.event.count"); v.Type() != pcommon.ValueTypeInt || v.Int() != 4 {
		t.Errorf("k8s.event.count = %v, want int 4", v.AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Errorf("severity = %v/%q, want WARN", lr.SeverityNumber(), lr.SeverityText())
	}
	if lr.Body().Str() != "K8S ADDED Event shop / web-1.18e1 BackOff" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestParity_Event_UnknownType_SeverityLeftToAdjustSwitch(t *testing.T) {
	cfg := config(func(c *Config) { c.OTTLParity = true; c.AdjustSeverity = false })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": eventObject("Other")}))

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Errorf("severity = %v, want unset", lr.SeverityNumber())
	}
}

func TestParity_Deleted_ActionIsDeleted(t *testing.T) {
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "DELETED", "object": podObject("Running")}))

	if got := attr(t, lr, "k8s.object.action").Str(); got != "DELETED" {
		t.Errorf("k8s.object.action = %q, want DELETED", got)
	}
	if got, want := lr.Body().Str(), "K8S DELETED Pod shop / web-1 Phase:Running"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestParity_EventWithEmptyMessage_BodyStillBuilt(t *testing.T) {
	obj := map[string]any{"kind": "Event", "type": "Normal", "metadata": map[string]any{"name": "e1", "namespace": "shop"}}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": obj}))

	if got, want := lr.Body().Str(), "K8S ADDED Event shop / e1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	absent(t, lr, "k8s.event.reason")
	absent(t, lr, "k8s.event.note")
}

func TestParity_NoActionNoMetadata_MessageIsKindOnly(t *testing.T) {
	obj := map[string]any{"kind": "Event", "type": "Normal"}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, obj))

	if got, want := lr.Body().Str(), "K8S Event"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	absent(t, lr, "k8s.object.action")
	absent(t, lr, "k8s.namespace")
}

func TestParity_StructuredFormat_ReadsActionFromStructured(t *testing.T) {
	body := map[string]any{"structured": map[string]any{"type": "ADDED", "object": podObject("Running")}}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, body))

	if got := attr(t, lr, "k8s.object.action").Str(); got != "ADDED" {
		t.Errorf("k8s.object.action = %q, want ADDED", got)
	}
}

func TestParity_NonStringLabel_IsCopiedAsIs(t *testing.T) {
	obj := podObject("Running")
	obj["metadata"] = map[string]any{"name": "p", "namespace": "n", "labels": map[string]any{"app": 7}}
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": obj}))

	if v := attr(t, lr, "k8s.labels.app"); v.Type() != pcommon.ValueTypeInt || v.Int() != 7 {
		t.Errorf("k8s.labels.app = %v, want int 7", v.AsRaw())
	}
}

func TestParity_ClusterNameIsNeverSet(t *testing.T) {
	cfg := config(func(c *Config) { c.OTTLParity = true })

	lr := process(t, cfg, recordWith(t, map[string]any{"type": "ADDED", "object": podObject("Running")}))

	absent(t, lr, "k8s.cluster.name")
}

func TestConfig_DefaultsKeepParityOff(t *testing.T) {
	cfg := config(nil)

	if cfg.OTTLParity || !cfg.RewriteMessages || !cfg.AdjustSeverity {
		t.Errorf("defaults = %+v, want parity off and both old switches on", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
}
