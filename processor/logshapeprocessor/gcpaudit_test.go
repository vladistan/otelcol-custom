// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const gcpAuditTestProject = "proj-example"

func gcpAuditConfig() *Config {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{gcpAuditTestProject: "test-kube"}
	return cfg
}

func gcpAuditInput(attrs map[string]any, body string) input {
	a := map[string]any{"log.source": "gcp.audit"}
	for k, v := range attrs {
		a[k] = v
	}
	return input{
		body:     body,
		attrs:    a,
		resource: map[string]any{"gcp.label.project_id": gcpAuditTestProject, "service.name": "gcp-audit"},
	}
}

func gcpAuditK8sAttrs(email string, extra map[string]any) map[string]any {
	a := map[string]any{
		"user.email":             email,
		"gcp.audit.service.name": "k8s.io",
		"gcp.audit.method.name":  "io.k8s.coordination.v1.leases.update",
	}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestConsume_GCPAudit_K8sRecord_ShapesEveryField(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())

	lr, res := h.run(gcpAuditInput(
		gcpAuditK8sAttrs("service-123456@container-engine-robot.iam.gserviceaccount.com", nil),
		"io.k8s.coordination.v1.leases.update coordination.k8s.io/v1/namespaces/kube-system/leases/test-lease"))

	if got := lr.Body().Str(); got != "io.k8s.coordination.v1.leases.update coordination.k8s.io/v1/namespaces/kube-system/leases/test-lease" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	r := res.Attributes()
	if attrStr(t, r, "service.name") != "gke-robot" || attrStr(t, r, "host.name") != "test-kube" || attrStr(t, r, "cloud.account.name") != "test-kube" {
		t.Fatalf("resource = %v", r.AsRaw())
	}
	if attrStr(t, lr.Attributes(), "log.source") != "gcp.audit" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestConsume_GCPAudit_FailedStatusWithName_AppendsNameAndIsError(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := gcpAuditK8sAttrs("system:addon-manager", map[string]any{"rpc.status_code": "5", "user.email": "system:addon-manager"})

	lr, res := h.run(gcpAuditInput(attrs, "v1.compute.backendServices.get"))

	if got := lr.Body().Str(); got != "v1.compute.backendServices.get NOT_FOUND" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberError || lr.SeverityText() != "ERROR" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	if attrStr(t, res.Attributes(), "service.name") != "addon-manager" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestConsume_GCPAudit_OldStatusNameAsInt_IsError(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := gcpAuditK8sAttrs("a@example.invalid", map[string]any{"rpc.response.status_code": int64(10)})

	lr, _ := h.run(gcpAuditInput(attrs, "leases.create"))

	if got := lr.Body().Str(); got != "leases.create ABORTED" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("severity = %d", lr.SeverityNumber())
	}
}

func TestConsume_GCPAudit_StatusZero_KeepsBodyAndInfo(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := gcpAuditK8sAttrs("a@example.invalid", map[string]any{"rpc.status_code": "0"})

	lr, _ := h.run(gcpAuditInput(attrs, "pods.get"))

	if lr.Body().Str() != "pods.get" || lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Fatalf("body %q severity %d", lr.Body().Str(), lr.SeverityNumber())
	}
}

func TestConsume_GCPAudit_UnknownStatusNumber_UsesTheNumber(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())

	lr, _ := h.run(gcpAuditInput(map[string]any{"rpc.status_code": "99"}, "x.y"))

	if lr.Body().Str() != "x.y 99" || lr.SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("body %q severity %d", lr.Body().Str(), lr.SeverityNumber())
	}
}

func TestConsume_GCPAudit_NonK8sRowWithoutSeverity_StaysUnsetAndHasNoHost(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := map[string]any{"user.email": "admin@example.invalid", "gcp.audit.service.name": "compute.googleapis.com"}

	lr, res := h.run(gcpAuditInput(attrs, "v1.compute.instances.list"))

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	absent(t, res.Attributes(), "host.name")
	if attrStr(t, res.Attributes(), "service.name") != "admin" || attrStr(t, res.Attributes(), "cloud.account.name") != "test-kube" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestConsume_GCPAudit_K8sClusterResourceType_CountsAsK8sRow(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	in := gcpAuditInput(map[string]any{"user.email": "a@example.invalid"}, "pods.get")
	in.resource["gcp.resource_type"] = "k8s_cluster"

	lr, res := h.run(in)

	if attrStr(t, res.Attributes(), "host.name") != "test-kube" || lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Fatalf("resource %v severity %d", res.Attributes().AsRaw(), lr.SeverityNumber())
	}
}

func TestConsume_GCPAudit_PrincipalShortening_Cases(t *testing.T) {
	cases := map[string]string{
		"service-9@container-engine-robot.iam.gserviceaccount.com": "gke-robot",
		"system:addon-manager":     "addon-manager",
		"system:kube-scheduler":    "kube-scheduler",
		"deployer@example.invalid": "deployer",
		"not-an-email":             "not-an-email",
		"a-very-long-service-account-name-that-keeps-going@example.invalid": "a-very-long-service-account-name",
	}
	for in, want := range cases {
		h := newHarness(t, gcpAuditConfig())
		_, res := h.run(gcpAuditInput(map[string]any{"user.email": in}, "x"))
		got := attrStr(t, res.Attributes(), "service.name")
		if got != want {
			t.Errorf("principal %q: service.name = %q, want %q", in, got, want)
		}
	}
}

func TestConsume_GCPAudit_NoAliasListed_HostIsProjectAndNoAccountName(t *testing.T) {
	h := newHarness(t, defaultConfig())

	_, res := h.run(gcpAuditInput(gcpAuditK8sAttrs("a@example.invalid", nil), "pods.get"))

	if attrStr(t, res.Attributes(), "host.name") != gcpAuditTestProject {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	absent(t, res.Attributes(), "cloud.account.name")
}

func TestConsume_GCPAudit_ProjectFromAccountIDFallback_IsUsed(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	in := gcpAuditInput(gcpAuditK8sAttrs("a@example.invalid", nil), "pods.get")
	in.resource = map[string]any{"cloud.account.id": gcpAuditTestProject}

	_, res := h.run(in)

	if attrStr(t, res.Attributes(), "host.name") != "test-kube" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestConsume_GCPAudit_EmptyBodyWithMethodAndResource_ComposesBody(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := map[string]any{"gcp.audit.method.name": "storage.objects.get", "gcp.audit.resource.name": "projects/_/buckets/test-bucket"}

	lr, _ := h.run(gcpAuditInput(attrs, ""))

	if lr.Body().Str() != "storage.objects.get projects/_/buckets/test-bucket" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_GCPAudit_Malformed_DoesNotPanic(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := map[string]any{
		"user.email":             int64(7),
		"rpc.status_code":        []any{"x"},
		"gcp.audit.service.name": true,
	}
	in := gcpAuditInput(attrs, "")
	in.resource = map[string]any{"gcp.label.project_id": int64(3)}

	lr, res := h.run(in)

	if lr.Body().Str() != "" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	absent(t, res.Attributes(), "host.name", "cloud.account.name")
}

func TestConsume_GCPAudit_EmptyStrings_DoNotPanic(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	in := gcpAuditInput(map[string]any{"user.email": "", "rpc.status_code": ""}, "")
	in.resource = map[string]any{}

	lr, _ := h.run(in)

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("severity = %d", lr.SeverityNumber())
	}
}

func TestConsume_GCPAudit_OtherLogSource_PassesUnmatched(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	in := gcpAuditInput(gcpAuditK8sAttrs("a@example.invalid", nil), "pods.get")
	in.attrs["log.source"] = "gcp.cloudrun"

	lr, res := h.run(in)

	if lr.Body().Str() != "pods.get" || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: %q %d", lr.Body().Str(), lr.SeverityNumber())
	}
	if attrStr(t, res.Attributes(), "service.name") != "gcp-audit" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); got["gcp.cloudrun"] != 1 {
		t.Fatalf("unmatched counter = %v", got)
	}
}

func TestConsume_GCPAudit_Batch_CountInEqualsOut(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	_ = rl.Resource().Attributes().FromRaw(map[string]any{"gcp.label.project_id": gcpAuditTestProject})
	lrs := rl.ScopeLogs().AppendEmpty().LogRecords()
	const n = 50
	for range n {
		lr := lrs.AppendEmpty()
		lr.Body().SetStr("io.k8s.coordination.v1.leases.update")
		_ = lr.Attributes().FromRaw(gcpAuditK8sAttrs("system:kube-controller-manager", map[string]any{"log.source": "gcp.audit"}))
	}

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	out := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	if out.Len() != n {
		t.Fatalf("records out = %d, want %d", out.Len(), n)
	}
	for i := range out.Len() {
		if out.At(i).SeverityNumber() != plog.SeverityNumberInfo {
			t.Fatalf("record %d severity = %d", i, out.At(i).SeverityNumber())
		}
	}
}

func TestConsume_GCPAudit_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"proj-a": "alias-a", "proj-b": "alias-b"}
	h := newHarness(t, cfg)
	rec := func(body, email, project string) auditRec {
		a := gcpAuditK8sAttrs(email, map[string]any{"log.source": "gcp.audit", "gcp.label.project_id": project})
		return auditRec{body: body, attrs: a}
	}

	out := h.auditBatch(map[string]any{"service.name": "gcp-audit"}, []auditRec{
		rec("one", "system:kube-scheduler", "proj-a"),
		rec("two", "deployer@example.invalid", "proj-b"),
		rec("three", "system:addon-manager", "proj-a"),
	})

	type want struct{ body, service, host string }
	wants := []want{{"one", "kube-scheduler", "alias-a"}, {"two", "deployer", "alias-b"}, {"three", "addon-manager", "alias-a"}}
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

func TestConsume_GCPAudit_BodyAlreadyEndsWithStatus_IsNotAppendedAgain(t *testing.T) {
	h := newHarness(t, gcpAuditConfig())
	attrs := gcpAuditK8sAttrs("a@example.invalid", map[string]any{"rpc.status_code": "5"})

	lr, _ := h.run(gcpAuditInput(attrs, "v1.compute.backendServices.get NOT_FOUND"))

	if got := lr.Body().Str(); got != "v1.compute.backendServices.get NOT_FOUND" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_GCPAudit_SecondPass_ChangesNothing(t *testing.T) {
	recs := []auditRec{
		{body: "v1.compute.backendServices.get", attrs: gcpAuditK8sAttrs("a@example.invalid", map[string]any{"log.source": "gcp.audit", "rpc.status_code": "5"})},
		{body: "", attrs: map[string]any{"log.source": "gcp.audit", "gcp.audit.method.name": "storage.objects.get", "gcp.audit.resource.name": "projects/_/buckets/test-bucket"}},
		{body: "pods.get", attrs: gcpAuditK8sAttrs("a@example.invalid", map[string]any{"log.source": "gcp.audit"})},
	}
	assertTwoPassesEqual(t, gcpAuditConfig(), map[string]any{"gcp.label.project_id": gcpAuditTestProject}, recs)
}
