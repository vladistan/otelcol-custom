// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore gcfsd

package logshapeprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// k8sOut is one output record with the attributes of the resource it ended up in.
type k8sOut struct {
	group int
	res   pcommon.Map
	lr    plog.LogRecord
}

// k8sRunBatch sends all records in ONE ResourceLogs and returns every output record in
// output order, tagged with the index of its output resource.
func k8sRunBatch(t *testing.T, resource map[string]any, ins ...input) []k8sOut {
	t.Helper()
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(resource); err != nil {
		t.Fatalf("resource: %v", err)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for _, in := range ins {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr(in.body)
		if err := lr.Attributes().FromRaw(in.attrs); err != nil {
			t.Fatalf("attrs: %v", err)
		}
	}
	h := newHarness(t, defaultConfig())
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	var outs []k8sOut
	all := h.sink.AllLogs()
	if len(all) != 1 {
		t.Fatalf("batches = %d, want 1", len(all))
	}
	for g := range all[0].ResourceLogs().Len() {
		out := all[0].ResourceLogs().At(g)
		for j := range out.ScopeLogs().Len() {
			recs := out.ScopeLogs().At(j).LogRecords()
			for k := range recs.Len() {
				outs = append(outs, k8sOut{group: g, res: out.Resource().Attributes(), lr: recs.At(k)})
			}
		}
	}
	if len(outs) != len(ins) {
		t.Fatalf("records out = %d, want %d", len(outs), len(ins))
	}
	return outs
}

func k8sWantGroups(t *testing.T, outs []k8sOut, groups ...int) {
	t.Helper()
	for i, want := range groups {
		if outs[i].group != want {
			t.Fatalf("record %d is in resource %d, want %d (body %q)", i, outs[i].group, want, outs[i].lr.Body().Str())
		}
	}
}

func k8sPodRow(pod, node, text string) input {
	in := k8sPodStatusInput("Pod team-a/" + pod + ": " + text + " on " + node)
	in.attrs["k8s.pod.name"] = pod
	in.attrs["k8s.node.name"] = node
	return in
}

func TestConsume_K8s_Batch_PodStatusKeepsEachPod(t *testing.T) {
	outs := k8sRunBatch(t, map[string]any{"k8s.cluster.name": "cluster-1"},
		k8sPodRow("pod-one", "node-a", "Running (1/1 ready)"),
		k8sPodRow("pod-two", "node-b", "Pending"),
		k8sPodRow("pod-one", "node-a", "Succeeded (0/1 ready)"),
	)
	k8sWantGroups(t, outs, 0, 0, 1)
	// pod-one's two rows share a resource; pod-two gets its own.
	k8sWant(t, "first body", outs[0].lr.Body().Str(), "Running (1/1 ready)")
	k8sWant(t, "second body", outs[1].lr.Body().Str(), "Succeeded (0/1 ready)")
	k8sWant(t, "third body", outs[2].lr.Body().Str(), "Pending")
	for i, w := range []struct{ pod, node string }{{"pod-one", "node-a"}, {"pod-one", "node-a"}, {"pod-two", "node-b"}} {
		k8sWant(t, "k8s.pod.name", attrStr(t, outs[i].res, "k8s.pod.name"), w.pod)
		k8sWant(t, "service.name", attrStr(t, outs[i].res, "service.name"), w.pod)
		k8sWant(t, "host.name", attrStr(t, outs[i].res, "host.name"), w.node)
		k8sWant(t, "k8s.node.name", attrStr(t, outs[i].res, "k8s.node.name"), w.node)
		k8sWant(t, "cluster kept", attrStr(t, outs[i].res, "k8s.cluster.name"), "cluster-1")
	}
}

func TestConsume_K8s_Batch_EventJSONKeepsEachEvent(t *testing.T) {
	outs := k8sRunBatch(t, map[string]any{"gcp.project": "proj-1"},
		input{body: k8sEventJSON("Warning", "BackOff", "restarting")},
		input{body: k8sNormalEvent},
	)
	k8sWantGroups(t, outs, 0, 1)
	k8sWant(t, "service.name 0", attrStr(t, outs[0].res, "service.name"), "Deployment/web")
	k8sWant(t, "host.name 0", attrStr(t, outs[0].res, "host.name"), "node-b")
	k8sWant(t, "service.name 1", attrStr(t, outs[1].res, "service.name"), "RedirectService/default")
	k8sWant(t, "host.name 1", attrStr(t, outs[1].res, "host.name"), "node-a")
	k8sWant(t, "namespace 0", attrStr(t, outs[0].res, "k8s.namespace.name"), "team-a")
	k8sWant(t, "namespace 1", attrStr(t, outs[1].res, "k8s.namespace.name"), "kube-system")
	k8sWant(t, "severity 0", outs[0].lr.SeverityText(), "WARN")
	k8sWant(t, "severity 1", outs[1].lr.SeverityText(), "INFO")
	k8sWant(t, "project kept", attrStr(t, outs[1].res, "gcp.project"), "proj-1")
}

func k8sNodeEventRow(node, typ string) input {
	in := k8sNodeEventInput()
	in.attrs["k8s.event.regarding.name"] = node
	in.attrs["k8s.event.type"] = typ
	in.attrs["k8s.name"] = node + ".18e95093bb0b3a11"
	return in
}

func TestConsume_K8s_Batch_EventAttributesKeepEachEvent(t *testing.T) {
	outs := k8sRunBatch(t, map[string]any{"k8s.namespace.name": "default", "service.name": "k8s-events"},
		k8sNodeEventRow("node-a", "Normal"),
		k8sNodeEventRow("node-b", "Warning"),
	)
	k8sWantGroups(t, outs, 0, 1)
	for i, node := range []string{"node-a", "node-b"} {
		k8sWant(t, "service.name", attrStr(t, outs[i].res, "service.name"), "Node/"+node)
		k8sWant(t, "k8s.node.name", attrStr(t, outs[i].res, "k8s.node.name"), node)
		k8sWant(t, "host.name", attrStr(t, outs[i].res, "host.name"), node)
	}
	k8sWant(t, "severity 0", outs[0].lr.SeverityText(), "INFO")
	k8sWant(t, "severity 1", outs[1].lr.SeverityText(), "WARN")
}

func TestConsume_K8s_Batch_LiftKeepsEachPod(t *testing.T) {
	rec := func(pod, ns string) input {
		return input{body: "plain " + pod, attrs: map[string]any{"k8s.namespace.name": ns, "k8s.pod.name": pod, "keep": pod}}
	}
	outs := k8sRunBatch(t, map[string]any{"k8s.cluster.name": "cluster-1"},
		rec("web-1", "team-a"), rec("web-2", "team-b"), rec("web-1", "team-a"))
	k8sWantGroups(t, outs, 0, 0, 1)
	// order inside the web-1 group is the input order
	k8sWant(t, "body 0", outs[0].lr.Body().Str(), "plain web-1")
	k8sWant(t, "body 1", outs[1].lr.Body().Str(), "plain web-1")
	k8sWant(t, "body 2", outs[2].lr.Body().Str(), "plain web-2")
	for i, w := range []struct{ pod, ns string }{{"web-1", "team-a"}, {"web-1", "team-a"}, {"web-2", "team-b"}} {
		k8sWant(t, "k8s.pod.name", attrStr(t, outs[i].res, "k8s.pod.name"), w.pod)
		k8sWant(t, "k8s.namespace.name", attrStr(t, outs[i].res, "k8s.namespace.name"), w.ns)
		k8sWant(t, "cluster kept", attrStr(t, outs[i].res, "k8s.cluster.name"), "cluster-1")
		k8sWant(t, "record attribute kept", attrStr(t, outs[i].lr.Attributes(), "keep"), w.pod)
	}
}

func k8sSeverityIs(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %v %q, want %v %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func TestConsume_K8s_Severity_PodRowWatchActionIsNotAType(t *testing.T) {
	in := k8sPodStatusInput("Pod team-a/job-1-abcde: Failed (0/1 ready) on node-a")
	for _, preset := range []plog.SeverityNumber{plog.SeverityNumberUnspecified, plog.SeverityNumberInfo} {
		lr, res := k8sRunSeverity(t, in, preset, "")
		k8sSeverityIs(t, lr, plog.SeverityNumberUnspecified, "")
		k8sWant(t, "pod-status path", attrStr(t, lr.Attributes(), "k8s.pod.status.phase"), "Failed")
		k8sWant(t, "service.name is the pod", attrStr(t, res.Attributes(), "service.name"), "job-1-abcde")
	}
}

func TestConsume_K8s_Severity_NodeEventRowTypes(t *testing.T) {
	for typ, want := range map[string]string{"Normal": "INFO", "Warning": "WARN", "MODIFIED": "", "Odd": ""} {
		lr, _ := newHarness(t, defaultConfig()).run(k8sNodeEventRow("node-a", typ))
		k8sWant(t, "severity text for "+typ, lr.SeverityText(), want)
	}
}

func TestConsume_K8s_Severity_NonEventObjectTypeIgnored(t *testing.T) {
	in := input{
		body: "text",
		attrs: map[string]any{
			"k8s.object.kind": "Pod", "k8s.event.reason": "Started", "k8s.event.type": "Warning",
			"k8s.event.regarding.kind": "Pod", "k8s.event.regarding.name": "web-1",
		},
	}
	lr, _ := newHarness(t, defaultConfig()).run(in)
	k8sSeverityIs(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestConsume_K8s_Cost_OversizeEventJSONPassesUnmatched(t *testing.T) {
	big := `{"involvedObject":{"kind":"Pod","name":"p"},"reason":"R","message":"` + strings.Repeat("x", k8sMaxDecodeBytes) + `"}`
	lr, res := newHarness(t, defaultConfig()).run(input{body: big})
	k8sWant(t, "body unchanged", lr.Body().Str(), big)
	absent(t, res.Attributes(), "service.name", "k8s.pod.name")
}

func TestConsume_K8s_Cost_EventAtTheCapStillShapes(t *testing.T) {
	pad := strings.Repeat("x", k8sMaxDecodeBytes/2)
	doc := `{"involvedObject":{"kind":"Pod","name":"p"},"reason":"R","message":"` + pad + `"}`
	lr, res := newHarness(t, defaultConfig()).run(input{body: doc})
	k8sWant(t, "body", lr.Body().Str(), "R: "+pad)
	k8sWant(t, "service.name", attrStr(t, res.Attributes(), "service.name"), "Pod/p")
}

func TestConsume_K8s_Cost_JSONWithoutEventKeysIsNotDecoded(t *testing.T) {
	doc := `{"a":"` + strings.Repeat("x", 100<<10) + `"}`
	if k8sHasEventKeys(doc) {
		t.Fatal("k8sHasEventKeys true for a body without event keys")
	}
	if !k8sHasEventKeys(k8sNormalEvent) || !k8sHasEventKeys(`{"reason":"R"}`) {
		t.Fatal("k8sHasEventKeys false for a real event")
	}
	if newK8sBranch(K8sConfig{}).claims(&rec{lr: plog.NewLogRecord(), body: doc}) {
		t.Fatal("claims true for a JSON body without event keys")
	}
	lr, res := newHarness(t, defaultConfig()).run(input{body: doc})
	k8sWant(t, "body unchanged", lr.Body().Str(), doc)
	absent(t, res.Attributes(), "service.name", "k8s.pod.name")
}
