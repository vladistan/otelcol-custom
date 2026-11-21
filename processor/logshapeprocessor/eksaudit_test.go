// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore vstan

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const eksAuditTestGroup = "/aws/eks/test-cluster/cluster"

func eksAuditConfig() *Config {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"test-cluster": "test-eks"}
	return cfg
}

func eksAuditInput(body string) input {
	return input{
		body:  body,
		attrs: map[string]any{"log.source": "aws.eks", "event.category": "audit"},
		resource: map[string]any{
			"cloudwatch.log.group.name": eksAuditTestGroup,
			"cloudwatch.log.stream":     "kube-apiserver-audit-0001",
			"service.name":              "aws.eks",
		},
	}
}

func eksAuditJSON(level, verb, user, resource, ns, name string, code int) string {
	return fmt.Sprintf(`{"kind":"Event","level":%q,"auditID":"11111111-2222-3333-4444-555555555555","stage":"ResponseComplete",`+
		`"requestURI":"/apis/coordination.k8s.io/v1/namespaces/%s/%s/%s","verb":%q,"user":{"username":%q},`+
		`"objectRef":{"resource":%q,"namespace":%q,"name":%q},"responseStatus":{"code":%d}}`,
		level, ns, resource, name, verb, user, resource, ns, name, code)
}

func TestConsume_EKSAudit_LeaseRecord_ShapesEveryField(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	raw := eksAuditJSON("Metadata", "get", "system:serviceaccount:kube-system:test-autoscaler", "leases", "kube-system", "eks-coredns-autoscaler", 200)

	lr, res := h.run(eksAuditInput(raw))

	if got := lr.Body().Str(); got != "GET leases kube-system/eks-coredns-autoscaler 200" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %d %q (level must not leak)", lr.SeverityNumber(), lr.SeverityText())
	}
	r, a := res.Attributes(), lr.Attributes()
	if attrStr(t, r, "service.name") != "test-autoscaler" || attrStr(t, r, "host.name") != "test-eks" {
		t.Fatalf("resource = %v", r.AsRaw())
	}
	absent(t, r, "cloud.account.name")
	attrInt(t, a, "http.response.status_code", 200)
	if attrStr(t, a, "http.request.method") != "GET" || attrStr(t, a, "vstan.audit.level") != "Metadata" ||
		attrStr(t, a, "log.record.original") != raw || attrStr(t, a, "log.source") != "aws.eks" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestConsume_EKSAudit_StatusClasses_MapToSeverity(t *testing.T) {
	cases := []struct {
		code int
		want plog.SeverityNumber
		text string
	}{
		{200, plog.SeverityNumberInfo, "INFO"},
		{302, plog.SeverityNumberInfo, "INFO"},
		{403, plog.SeverityNumberWarn, "WARN"},
		{404, plog.SeverityNumberWarn, "WARN"},
		{500, plog.SeverityNumberError, "ERROR"},
		{503, plog.SeverityNumberError, "ERROR"},
		{0, plog.SeverityNumberUnspecified, ""},
	}
	for _, c := range cases {
		h := newHarness(t, eksAuditConfig())
		lr, _ := h.run(eksAuditInput(eksAuditJSON("RequestResponse", "list", "alice", "pods", "default", "p", c.code)))
		if lr.SeverityNumber() != c.want || lr.SeverityText() != c.text {
			t.Errorf("status %d: severity = %d %q, want %d %q", c.code, lr.SeverityNumber(), lr.SeverityText(), c.want, c.text)
		}
	}
}

func TestConsume_EKSAudit_NoResponseStatus_StaysUnsetAndBodyHasNoStatus(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	raw := `{"level":"Metadata","verb":"watch","user":{"username":"alice"},"objectRef":{"resource":"pods","namespace":"default"}}`

	lr, _ := h.run(eksAuditInput(raw))

	if lr.Body().Str() != "WATCH pods default" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("severity = %d", lr.SeverityNumber())
	}
	absent(t, lr.Attributes(), "http.response.status_code")
}

func TestConsume_EKSAudit_NoObjectRef_UsesRequestURI(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	raw := `{"level":"Metadata","verb":"get","requestURI":"/healthz","user":{"username":"alice"},"responseStatus":{"code":"200"}}`

	lr, _ := h.run(eksAuditInput(raw))

	if lr.Body().Str() != "GET /healthz 200" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_EKSAudit_SubresourceAndClusterScoped_BuildTarget(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	raw := `{"verb":"update","user":{"username":"alice"},"objectRef":{"resource":"pods","subresource":"status","namespace":"default","name":"p"},"responseStatus":{"code":200}}`
	lr, _ := h.run(eksAuditInput(raw))
	if lr.Body().Str() != "UPDATE pods/status default/p 200" {
		t.Fatalf("body = %q", lr.Body().Str())
	}

	raw = `{"verb":"get","user":{"username":"alice"},"objectRef":{"resource":"nodes","name":"n1"},"responseStatus":{"code":200}}`
	lr, _ = h.run(eksAuditInput(raw))
	if lr.Body().Str() != "GET nodes n1 200" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_EKSAudit_PrincipalShortening_Cases(t *testing.T) {
	cases := map[string]string{
		"system:node:ip-10-0-0-1":                              "node-ip-10-0-0-1",
		"system:kube-scheduler":                                "kube-scheduler",
		"system:serviceaccount:kube-system:aws-node":           "aws-node",
		"arn:aws:sts::111122223333:assumed-role/Ops/session-1": "session-1",
		"a-very-long-user-name-that-keeps-going-and-going":     "a-very-long-user-name-that-keeps",
	}
	for user, want := range cases {
		h := newHarness(t, eksAuditConfig())
		_, res := h.run(eksAuditInput(eksAuditJSON("Metadata", "get", user, "pods", "d", "p", 200)))
		if got := attrStr(t, res.Attributes(), "service.name"); got != want {
			t.Errorf("user %q: service.name = %q, want %q", user, got, want)
		}
	}
}

func TestConsume_EKSAudit_ClusterAlias_PresentOrAbsent(t *testing.T) {
	h := newHarness(t, defaultConfig())
	_, res := h.run(eksAuditInput(eksAuditJSON("Metadata", "get", "alice", "pods", "d", "p", 200)))
	if attrStr(t, res.Attributes(), "host.name") != "test-cluster" {
		t.Fatalf("no alias: resource = %v", res.Attributes().AsRaw())
	}

	in := eksAuditInput(eksAuditJSON("Metadata", "get", "alice", "pods", "d", "p", 200))
	in.resource["cloudwatch.log.group.name"] = "/aws/other/path"
	_, res = newHarness(t, eksAuditConfig()).run(in)
	absent(t, res.Attributes(), "host.name")
}

func TestConsume_EKSAudit_Malformed_DoesNotPanic(t *testing.T) {
	bodies := []string{
		`{"verb":5,"user":"x","objectRef":[1],"responseStatus":{"code":{"a":1}},"level":3}`,
		`{"verb":"get","user":{"username":7},"responseStatus":{"code":"abc"}}`,
		`{"verb":""}`,
		`{"auditID":"x"}`,
		`{`,
		``,
		`[]`,
		`null`,
	}
	for _, b := range bodies {
		h := newHarness(t, eksAuditConfig())
		lr, _ := h.run(eksAuditInput(b))
		if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
			t.Errorf("body %q: severity = %d", b, lr.SeverityNumber())
		}
	}
}

func TestConsume_EKSAudit_NonJSONBody_PassesUnmatched(t *testing.T) {
	h := newHarness(t, eksAuditConfig())

	lr, res := h.run(eksAuditInput("plain text line"))

	if lr.Body().Str() != "plain text line" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	absent(t, res.Attributes(), "host.name")
	if got := h.counter(unmatchedMetric, "source"); got["aws.eks"] != 1 {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestConsume_EKSAudit_OtherLogSource_PassesUnmatched(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	in := eksAuditInput(eksAuditJSON("Metadata", "get", "alice", "pods", "d", "p", 200))
	in.attrs["log.source"] = "aws.cloudwatch"

	lr, res := h.run(in)

	if lr.Body().Str() != in.body || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: %q", lr.Body().Str())
	}
	absent(t, res.Attributes(), "host.name")
}

func TestConsume_EKSAudit_HeartbeatBatch_CountInEqualsOut(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	_ = rl.Resource().Attributes().FromRaw(eksAuditInput("").resource)
	lrs := rl.ScopeLogs().AppendEmpty().LogRecords()
	const n = 40
	for range n {
		lr := lrs.AppendEmpty()
		lr.Body().SetStr(eksAuditJSON("Metadata", "update", "system:kube-controller-manager", "leases", "kube-system", "kube-controller-manager", 200))
		_ = lr.Attributes().FromRaw(map[string]any{"log.source": "aws.eks"})
	}

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	out := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	if out.Len() != n {
		t.Fatalf("records out = %d, want %d", out.Len(), n)
	}
	for i := range out.Len() {
		if got := out.At(i).Body().Str(); got != "UPDATE leases kube-system/kube-controller-manager 200" {
			t.Fatalf("record %d body = %q", i, got)
		}
	}
}

func TestConsume_EKSAudit_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"cluster-a": "alias-a", "cluster-b": "alias-b"}
	h := newHarness(t, cfg)
	rec := func(user, cluster string) auditRec {
		return auditRec{
			body: eksAuditJSON("Metadata", "get", user, "pods", "default", user, 200),
			attrs: map[string]any{
				"log.source":                "aws.eks",
				"cloudwatch.log.group.name": "/aws/eks/" + cluster + "/cluster",
			},
		}
	}

	out := h.auditBatch(map[string]any{"service.name": "aws.eks"}, []auditRec{
		rec("alice", "cluster-a"), rec("bob", "cluster-b"), rec("carol", "cluster-a"),
	})

	wants := []struct{ body, service, host string }{
		{"GET pods default/alice 200", "alice", "alias-a"},
		{"GET pods default/bob 200", "bob", "alias-b"},
		{"GET pods default/carol 200", "carol", "alias-a"},
	}
	if len(out) != len(wants) {
		t.Fatalf("records out = %d", len(out))
	}
	for i, w := range wants {
		r := out[i].res.Attributes()
		if out[i].lr.Body().Str() != w.body || attrStr(t, r, "service.name") != w.service || attrStr(t, r, "host.name") != w.host {
			t.Errorf("record %d: body %q resource %v, want %+v", i, out[i].lr.Body().Str(), r.AsRaw(), w)
		}
	}
}

func TestConsume_EKSAudit_ExistingOriginal_IsKept(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	in := eksAuditInput(eksAuditJSON("Metadata", "get", "alice", "pods", "d", "p", 200))
	in.attrs["log.record.original"] = "first original"

	lr, _ := h.run(in)

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "first original" {
		t.Fatalf("log.record.original = %q", got)
	}
	if lr.Body().Str() != "GET pods d/p 200" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_EKSAudit_SecondPass_ChangesNothing(t *testing.T) {
	attrs := map[string]any{"log.source": "aws.eks"}
	recs := []auditRec{
		{body: eksAuditJSON("Metadata", "get", "alice", "pods", "d", "p", 200), attrs: attrs},
		{body: eksAuditJSON("Metadata", "list", "bob", "pods", "d", "p", 503), attrs: attrs},
		{body: "plain text line", attrs: attrs},
	}
	assertTwoPassesEqual(t, eksAuditConfig(), eksAuditInput("").resource, recs)
}

func TestConsume_EKSAudit_HugeBody_PassesUnmatchedQuickly(t *testing.T) {
	cfg := eksAuditConfig()
	cfg.Branches = []string{"eksaudit"}
	h := newHarness(t, cfg)
	huge := `{"verb":"get","pad":"` + strings.Repeat("x", 2<<20) + `"}`

	start := time.Now()
	lr, res := h.run(eksAuditInput(huge))
	elapsed := time.Since(start)

	if lr.Body().Str() != huge {
		t.Fatal("body changed")
	}
	absent(t, res.Attributes(), "host.name")
	if got := h.counter(unmatchedMetric, "source"); got["aws.eks"] != 1 {
		t.Fatalf("unmatched = %v", got)
	}
	if elapsed > time.Second {
		t.Fatalf("took %v", elapsed)
	}
}

func TestConsume_EKSAudit_JSONWithoutAuditKeys_PassesUnmatched(t *testing.T) {
	h := newHarness(t, eksAuditConfig())
	raw := `{"foo":"bar"}`

	lr, _ := h.run(eksAuditInput(raw))

	if lr.Body().Str() != raw {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if got := h.counter(unmatchedMetric, "source"); got["aws.eks"] != 1 {
		t.Fatalf("unmatched = %v", got)
	}
}
