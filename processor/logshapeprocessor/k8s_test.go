// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const k8sNormalEvent = `{"apiVersion":"v1","involvedObject":{"kind":"RedirectService","name":"default","namespace":"kube-system"},` +
	`"kind":"Event","message":"deleted redirect service obj default","metadata":{"name":"default.18e952fe67152ff9","namespace":"kube-system"},` +
	`"reason":"UpdateRedirectService","source":{"component":"redirect-service-controller","host":"node-a"},"type":"Normal"}`

func k8sEventJSON(typ, reason, message string) string {
	return `{"involvedObject":{"kind":"Deployment","name":"web","namespace":"team-a"},"reason":"` + reason +
		`","message":"` + message + `","type":"` + typ + `","source":{"host":"node-b"}}`
}

func k8sPodStatusInput(body string) input {
	return input{
		body: body,
		attrs: map[string]any{
			"event.domain":         "k8s",
			"k8s.event.type":       "MODIFIED",
			"k8s.namespace.name":   "team-a",
			"k8s.node.name":        "node-a",
			"k8s.pod.name":         "job-1-abcde",
			"k8s.pod.namespace":    "team-a",
			"k8s.pod.phase":        "Succeeded",
			"k8s.pod.status.phase": "Succeeded",
			"k8s.resource.name":    "pods",
		},
		resource: map[string]any{"k8s.cluster.name": "cluster-1", "k8s.namespace.name": "team-a"},
	}
}

func k8sConfigWithStaticNodes(prefixes ...string) *Config {
	cfg := defaultConfig()
	cfg.K8s.StaticPodNodes = prefixes
	return cfg
}

// k8sRunSeverity sends one record that already carries a severity, as the receiver gives it.
func k8sRunSeverity(t *testing.T, in input, num plog.SeverityNumber, text string) (plog.LogRecord, pcommon.Resource) {
	t.Helper()
	h := newHarness(t, defaultConfig())
	ld := in.logs()
	lr := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	lr.SetSeverityNumber(num)
	lr.SetSeverityText(text)
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	out := h.sink.AllLogs()[0].ResourceLogs().At(0)
	return out.ScopeLogs().At(0).LogRecords().At(0), out.Resource()
}

func k8sWant(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

func TestConsume_K8s_PodStatus_LiftedAndShaped(t *testing.T) {
	in := k8sPodStatusInput("Pod team-a/job-1-abcde: Succeeded (0/1 ready) on node-a")
	lr, res := k8sRunSeverity(t, in, plog.SeverityNumberInfo, "INFO")
	ra, a := res.Attributes(), lr.Attributes()
	k8sWant(t, "body", lr.Body().Str(), "Succeeded (0/1 ready)")
	k8sWant(t, "k8s.namespace.name", attrStr(t, ra, "k8s.namespace.name"), "team-a")
	k8sWant(t, "k8s.pod.name", attrStr(t, ra, "k8s.pod.name"), "job-1-abcde")
	k8sWant(t, "k8s.node.name", attrStr(t, ra, "k8s.node.name"), "node-a")
	k8sWant(t, "k8s.cluster.name", attrStr(t, ra, "k8s.cluster.name"), "cluster-1")
	k8sWant(t, "host.name", attrStr(t, ra, "host.name"), "node-a")
	k8sWant(t, "service.name", attrStr(t, ra, "service.name"), "job-1-abcde")
	k8sWant(t, "phase", attrStr(t, a, "k8s.pod.status.phase"), "Succeeded")
	absent(t, a, "k8s.pod.phase", "k8s.namespace.name", "k8s.pod.name", "k8s.node.name")
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Fatalf("severity = %v %q, want unset", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestConsume_K8s_PodStatus_PhaseWithoutReady(t *testing.T) {
	in := k8sPodStatusInput("Pod team-a/job-1-abcde: Pending on node-a")
	lr, _ := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "body", lr.Body().Str(), "Pending")
	k8sWant(t, "phase", attrStr(t, lr.Attributes(), "k8s.pod.status.phase"), "Pending")
}

func TestConsume_K8s_PodStatus_NoNodeLeavesHostUnset(t *testing.T) {
	in := input{body: "Pod team-a/job-2-brave: Pending", resource: map[string]any{}}
	lr, res := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "body", lr.Body().Str(), "Pending")
	k8sWant(t, "k8s.namespace.name", attrStr(t, res.Attributes(), "k8s.namespace.name"), "team-a")
	absent(t, res.Attributes(), "host.name", "k8s.node.name")
}

func TestConsume_K8s_PodStatus_StaticPodNodeFromName(t *testing.T) {
	in := input{body: "Pod kube-system/kube-apiserver-node-c: Running (1/1 ready)"}
	_, res := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "host.name", attrStr(t, res.Attributes(), "host.name"), "node-c")
	k8sWant(t, "k8s.node.name", attrStr(t, res.Attributes(), "k8s.node.name"), "node-c")
}

func TestConsume_K8s_PodStatus_StaticPodWithoutSuffixLeavesHostUnset(t *testing.T) {
	in := input{body: "Pod kube-system/etcd-: Running"}
	_, res := newHarness(t, defaultConfig()).run(in)
	absent(t, res.Attributes(), "host.name", "k8s.node.name")
}

func TestConsume_K8s_PodStatus_ExtraStaticPrefixExtendsDefaults(t *testing.T) {
	in := input{body: "Pod kube-system/custom-ctl-node-d: Running (1/1 ready)"}
	_, res := newHarness(t, defaultConfig()).run(in)
	absent(t, res.Attributes(), "host.name")
	_, res = newHarness(t, k8sConfigWithStaticNodes("custom-ctl-")).run(in)
	k8sWant(t, "host.name", attrStr(t, res.Attributes(), "host.name"), "node-d")
	in = input{body: "Pod kube-system/etcd-node-e: Running"}
	_, res = newHarness(t, k8sConfigWithStaticNodes("custom-ctl-")).run(in)
	k8sWant(t, "default prefix kept", attrStr(t, res.Attributes(), "host.name"), "node-e")
}

func TestConsume_K8s_Event_NormalIsInfoWithEveryField(t *testing.T) {
	in := input{body: k8sNormalEvent, attrs: map[string]any{"log.record.uid": "u-1"}, resource: map[string]any{"gcp.project": "proj-1"}}
	lr, res := k8sRunSeverity(t, in, plog.SeverityNumberInfo, "INFO")
	ra := res.Attributes()
	k8sWant(t, "body", lr.Body().Str(), "UpdateRedirectService: deleted redirect service obj default")
	k8sWant(t, "host.name", attrStr(t, ra, "host.name"), "node-a")
	k8sWant(t, "service.name", attrStr(t, ra, "service.name"), "RedirectService/default")
	k8sWant(t, "k8s.namespace.name", attrStr(t, ra, "k8s.namespace.name"), "kube-system")
	k8sWant(t, "gcp.project kept", attrStr(t, ra, "gcp.project"), "proj-1")
	k8sWant(t, "uid kept", attrStr(t, lr.Attributes(), "log.record.uid"), "u-1")
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %v %q, want INFO", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestConsume_K8s_Event_WarningIsWarn(t *testing.T) {
	lr, res := newHarness(t, defaultConfig()).run(input{body: k8sEventJSON("Warning", "BackOff", "Back-off restarting failed container")})
	k8sWant(t, "body", lr.Body().Str(), "BackOff: Back-off restarting failed container")
	k8sWant(t, "service.name", attrStr(t, res.Attributes(), "service.name"), "Deployment/web")
	k8sWant(t, "host.name", attrStr(t, res.Attributes(), "host.name"), "node-b")
	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Fatalf("severity = %v %q, want WARN", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestConsume_K8s_Event_OtherTypeClearsSeverity(t *testing.T) {
	for _, typ := range []string{"Odd", ""} {
		lr, _ := k8sRunSeverity(t, input{body: k8sEventJSON(typ, "Thing", "happened")}, plog.SeverityNumberInfo, "INFO")
		if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
			t.Fatalf("type %q: severity = %v %q, want unset", typ, lr.SeverityNumber(), lr.SeverityText())
		}
		k8sWant(t, "body", lr.Body().Str(), "Thing: happened")
	}
}

func TestConsume_K8s_Event_ReasonOnlyBody(t *testing.T) {
	doc := `{"involvedObject":{"kind":"Pod","name":"p-1","namespace":"team-a"},"reason":"Created","type":"Normal"}`
	lr, res := newHarness(t, defaultConfig()).run(input{body: doc})
	k8sWant(t, "body", lr.Body().Str(), "Created")
	k8sWant(t, "k8s.pod.name", attrStr(t, res.Attributes(), "k8s.pod.name"), "p-1")
	absent(t, res.Attributes(), "host.name")
}

func TestConsume_K8s_Event_ImageStreamingJSONNoteStaysInfo(t *testing.T) {
	note := `{\"method\":\"AuthImage\",\"code\":\"UNAUTHENTICATED\",\"message\":\"Failed to configure/retrieve credentials.\"}`
	doc := `{"involvedObject":{"kind":"Node","name":"node-a"},"reason":"ImageStreaming","message":"` + note + `","type":"Normal","source":{"component":"gcfsd","host":"node-a"}}`
	lr, res := newHarness(t, defaultConfig()).run(input{body: doc})
	k8sWant(t, "body", lr.Body().Str(), "ImageStreaming: Failed to configure/retrieve credentials (UNAUTHENTICATED)")
	k8sWant(t, "service.name", attrStr(t, res.Attributes(), "service.name"), "Node/node-a")
	k8sWant(t, "k8s.node.name", attrStr(t, res.Attributes(), "k8s.node.name"), "node-a")
	k8sWant(t, "severity text", lr.SeverityText(), "INFO")
}

func k8sNodeEventInput() input {
	return input{
		body: "K8S MODIFIED Event default / node-a.18e95093bb0b3a11 ImageStreaming",
		attrs: map[string]any{
			"event.domain":             "k8s",
			"k8s.event.note":           `{"method":"AuthImage","code":"UNAUTHENTICATED","message":"Failed to configure/retrieve credentials.","info":{"reason":"ACCESS_TOKEN_EXPIRED"}}`,
			"k8s.event.reason":         "ImageStreaming",
			"k8s.event.regarding.kind": "Node",
			"k8s.event.regarding.name": "node-a",
			"k8s.event.source":         "gcfsd",
			"k8s.event.type":           "Normal",
			"k8s.resource.name":        "events",
			"k8s.object.kind":          "Event",
			"k8s.object.action":        "MODIFIED",
			"k8s.event.count":          int64(44),
			"message_type":             "k8sobject",
		},
		resource: map[string]any{"host.name": "node-a", "k8s.namespace.name": "default", "service.name": "k8s-events"},
	}
}

func TestConsume_K8s_Event_ImageStreamingAttributesStayInfo(t *testing.T) {
	lr, res := k8sRunSeverity(t, k8sNodeEventInput(), plog.SeverityNumberInfo, "INFO")
	k8sWant(t, "body", lr.Body().Str(), "ImageStreaming: Failed to configure/retrieve credentials (UNAUTHENTICATED)")
	k8sWant(t, "service.name", attrStr(t, res.Attributes(), "service.name"), "Node/node-a")
	k8sWant(t, "host.name", attrStr(t, res.Attributes(), "host.name"), "node-a")
	k8sWant(t, "source kept", attrStr(t, lr.Attributes(), "k8s.event.source"), "gcfsd")
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %v %q, want INFO", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestConsume_K8s_Event_AttributesWarningWithPlainNote(t *testing.T) {
	in := input{
		body: "Pod team-a/web-1: Failed - Error: image not found",
		attrs: map[string]any{
			"k8s.event.reason":              "Failed",
			"k8s.event.type":                "Warning",
			"k8s.event.regarding.kind":      "Pod",
			"k8s.event.regarding.name":      "web-1",
			"k8s.event.regarding.namespace": "team-a",
		},
		resource: map[string]any{"k8s.cluster.name": "cluster-1"},
	}
	lr, res := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "body", lr.Body().Str(), "Failed: Error: image not found")
	k8sWant(t, "service.name", attrStr(t, res.Attributes(), "service.name"), "Pod/web-1")
	k8sWant(t, "k8s.namespace.name", attrStr(t, res.Attributes(), "k8s.namespace.name"), "team-a")
	k8sWant(t, "k8s.pod.name", attrStr(t, res.Attributes(), "k8s.pod.name"), "web-1")
	k8sWant(t, "severity text", lr.SeverityText(), "WARN")
}

func TestConsume_K8s_Lift_RecordAttributesMoveToResource(t *testing.T) {
	in := input{
		body:     "plain application text",
		attrs:    map[string]any{"k8s.namespace.name": "team-a", "pod": "web-2", "node": "node-a", "cluster": "cluster-1", "keep": "yes"},
		resource: map[string]any{"k8s.node.name": "node-z"},
	}
	lr, res := newHarness(t, defaultConfig()).run(in)
	ra := res.Attributes()
	k8sWant(t, "k8s.namespace.name", attrStr(t, ra, "k8s.namespace.name"), "team-a")
	k8sWant(t, "k8s.pod.name", attrStr(t, ra, "k8s.pod.name"), "web-2")
	k8sWant(t, "k8s.node.name resource wins", attrStr(t, ra, "k8s.node.name"), "node-z")
	k8sWant(t, "k8s.cluster.name", attrStr(t, ra, "k8s.cluster.name"), "cluster-1")
	k8sWant(t, "host.name", attrStr(t, ra, "host.name"), "node-z")
	k8sWant(t, "body untouched", lr.Body().Str(), "plain application text")
	k8sWant(t, "other attribute kept", attrStr(t, lr.Attributes(), "keep"), "yes")
	absent(t, lr.Attributes(), "k8s.namespace.name", "pod", "cluster")
}

func TestConsume_K8s_Lift_NoNamespaceWritesNone(t *testing.T) {
	in := input{body: "text", attrs: map[string]any{"k8s.pod.name": "web-3"}, resource: map[string]any{"host.name": "edge-1"}}
	_, res := newHarness(t, defaultConfig()).run(in)
	absent(t, res.Attributes(), "k8s.namespace.name")
	k8sWant(t, "host.name unchanged", attrStr(t, res.Attributes(), "host.name"), "edge-1")
}

func TestConsume_K8s_Lift_StaticPodInfersHost(t *testing.T) {
	in := input{body: "text", attrs: map[string]any{"k8s.namespace.name": "kube-system", "k8s.pod.name": "kube-scheduler-node-f"}}
	_, res := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "host.name", attrStr(t, res.Attributes(), "host.name"), "node-f")
	in = input{body: "text", attrs: map[string]any{"k8s.pod.name": "kube-scheduler-"}}
	_, res = newHarness(t, defaultConfig()).run(in)
	absent(t, res.Attributes(), "host.name")
}

func TestConsume_K8s_Unmatched_OrdinaryRecordsPassUnchanged(t *testing.T) {
	cases := []input{
		{body: "hello world", attrs: map[string]any{"a": "b"}},
		{body: `{"a":1}`},
		{body: `{"reason":"","involvedObject":{}}`},
		{body: "Pod is broken"},
		{body: "Pod team-a/x: running now (soon)"},
		{body: "K8S MODIFIED Event default / node-a.1 ImageStreaming"},
	}
	for _, in := range cases {
		lr, res := newHarness(t, defaultConfig()).run(in)
		k8sWant(t, "body", lr.Body().Str(), in.body)
		absent(t, res.Attributes(), "k8s.pod.name", "service.name", "host.name")
		absent(t, lr.Attributes(), "k8s.pod.status.phase")
	}
}

func TestConsume_K8s_Malformed_NeverPanics(t *testing.T) {
	huge := strings.Repeat("x", 2<<20)
	cases := []input{
		{body: ""},
		{body: "{"},
		{body: `{"involvedObject":{"kind":"Pod","name":"p"`},
		{body: `{"reason":5}`},
		{body: `{"involvedObject":"x","reason":["a"]}`},
		{body: `[1,2]`},
		{body: "Pod /: x"},
		{body: "Pod " + huge + "/" + huge + ": Running"},
		{body: `{"reason":"R","message":"` + huge + `"}`},
		{body: "{" + huge},
		{body: huge, attrs: map[string]any{"k8s.event.reason": int64(5), "k8s.event.note": []any{"x"}}},
		{body: "text", attrs: map[string]any{"k8s.event.reason": "R", "k8s.event.note": `{"code":5,"message":{}}`, "k8s.event.type": 7}},
		{body: "text", attrs: map[string]any{"k8s.namespace.name": int64(3), "k8s.pod.name": map[string]any{"a": "b"}}},
	}
	for _, in := range cases {
		newHarness(t, defaultConfig()).run(in)
	}
}

func TestConsume_K8s_Malformed_BadNoteKeepsText(t *testing.T) {
	in := input{body: "text", attrs: map[string]any{"k8s.event.reason": "R", "k8s.event.note": `{"code":5,"message":{}}`}}
	lr, _ := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "body", lr.Body().Str(), `R: {"code":5,"message":{}}`)
}

func TestConsume_K8s_PodStatus_WinsOverEventAttributes(t *testing.T) {
	in := k8sPodStatusInput("Pod team-a/job-1-abcde: Running (1/1 ready) on node-a")
	in.attrs["k8s.event.reason"] = "Started"
	lr, _ := newHarness(t, defaultConfig()).run(in)
	k8sWant(t, "body", lr.Body().Str(), "Running (1/1 ready)")
}
