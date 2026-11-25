// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func gcplbAttrs(over map[string]any) map[string]any {
	a := map[string]any{
		"log.source":                        "lb",
		"http.request.method":               "get",
		"url.domain":                        "grafana.admin.example.invalid",
		"url.path":                          "/api/live/ws",
		"http.response.status_code":         int64(101),
		"http.request.server.duration":      40.0,
		"gcp.load_balancing.status.details": "response_sent_by_backend",
	}
	for k, v := range over {
		if v == nil {
			delete(a, k)
			continue
		}
		a[k] = v
	}
	return a
}

func gcplbResource() map[string]any {
	return map[string]any{
		"gcp.label.project_id":           "example-project",
		"gcp.label.backend_service_name": "k8s1-0a1b2c3d-monitoring-grafana-3000-4e5f6a7b",
	}
}

func gcplbRun(t *testing.T, cfg *Config, in input) (plog.LogRecord, map[string]any) {
	t.Helper()
	h := newHarness(t, cfg)
	lr, res := h.run(in)
	return lr, res.Attributes().AsRaw()
}

func gcplbOnly(extra []string) *Config {
	cfg := defaultConfig()
	cfg.Branches = []string{"gcplb"}
	cfg.GCPLBErrorValues = extra
	return cfg
}

func gcplbSeverity(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %v %q, want %v %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func TestConsume_GCPLB_FullRecord_Shaped(t *testing.T) {
	lr, res := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(nil), resource: gcplbResource()})
	if got := lr.Body().Str(); got != "GET grafana.admin.example.invalid/api/live/ws 101 40s" {
		t.Fatalf("body = %q", got)
	}
	if res["host.name"] != "example-project" || res["service.name"] != "monitoring-grafana-3000" {
		t.Fatalf("resource = %v", res)
	}
	gcplbSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	if got := attrStr(t, lr.Attributes(), "url.path"); got != "/api/live/ws" {
		t.Fatalf("url.path = %q", got)
	}
	attrInt(t, lr.Attributes(), "http.response.status_code", 101)
}

func TestConsume_GCPLB_StatusClasses_Severity(t *testing.T) {
	cases := []struct {
		status int64
		num    plog.SeverityNumber
		text   string
	}{
		{200, plog.SeverityNumberInfo, "INFO"},
		{302, plog.SeverityNumberInfo, "INFO"},
		{404, plog.SeverityNumberWarn, "WARN"},
		{502, plog.SeverityNumberError, "ERROR"},
	}
	for _, c := range cases {
		lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"http.response.status_code": c.status}), resource: gcplbResource()})
		gcplbSeverity(t, lr, c.num, c.text)
	}
}

func TestConsume_GCPLB_NoStatus_SeverityUnsetBodyKept(t *testing.T) {
	lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"http.response.status_code": nil}), resource: gcplbResource()})
	gcplbSeverity(t, lr, plog.SeverityNumberUnspecified, "")
	if got := lr.Body().Str(); got != "GET grafana.admin.example.invalid/api/live/ws 40s" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_GCPLB_DefaultErrorDetails_Warn(t *testing.T) {
	for _, d := range []string{"unsupported_method", "invalid_http2_request_content_length"} {
		lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": d, "http.response.status_code": int64(200)}), resource: gcplbResource()})
		gcplbSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
	}
}

func TestConsume_GCPLB_ClientDisconnectedAndUnknownDetails_StayInfo(t *testing.T) {
	for _, d := range []string{"client_disconnected_after_partial_response", "something_new", ""} {
		lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": d, "http.response.status_code": int64(200)}), resource: gcplbResource()})
		gcplbSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	}
}

func TestConsume_GCPLB_ExtraConfiguredDetail_Warn(t *testing.T) {
	attrs := gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": "client_disconnected_after_partial_response", "http.response.status_code": int64(200)})
	lr, _ := gcplbRun(t, gcplbOnly([]string{"client_disconnected_after_partial_response"}), input{attrs: attrs, resource: gcplbResource()})
	gcplbSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
	lr, _ = gcplbRun(t, gcplbOnly([]string{"client_disconnected_after_partial_response"}), input{attrs: gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": "unsupported_method"}), resource: gcplbResource()})
	gcplbSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestConsume_GCPLB_ErrorDetailWithServerError_StaysError(t *testing.T) {
	lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": "unsupported_method", "http.response.status_code": int64(503)}), resource: gcplbResource()})
	gcplbSeverity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestConsume_GCPLB_ErrorDetailWithoutStatus_Warn(t *testing.T) {
	lr, _ := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"gcp.load_balancing.status.details": "unsupported_method", "http.response.status_code": nil}), resource: gcplbResource()})
	gcplbSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestConsume_GCPLB_EmptyBody_BuiltFromAttributes(t *testing.T) {
	lr, _ := gcplbRun(t, defaultConfig(), input{body: "", attrs: gcplbAttrs(nil), resource: gcplbResource()})
	if lr.Body().Str() == "" {
		t.Fatal("body empty")
	}
}

func TestConsume_GCPLB_ProjectFromAccountIDWhenNoLabel_HostSet(t *testing.T) {
	res := map[string]any{"cloud.account.id": "other-project"}
	_, got := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(nil), resource: res})
	if got["host.name"] != "other-project" {
		t.Fatalf("resource = %v", got)
	}
	if _, ok := got["service.name"]; ok {
		t.Fatalf("service.name set without a backend: %v", got)
	}
}

func TestConsume_GCPLB_OnlyBackendAndMethod_NonEmptyBody(t *testing.T) {
	attrs := map[string]any{"log.source": "lb", "http.request.method": "POST"}
	lr, _ := gcplbRun(t, defaultConfig(), input{attrs: attrs})
	if lr.Body().Str() != "POST" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_GCPLB_WrongTypedAttributes_NoPanic(t *testing.T) {
	attrs := gcplbAttrs(map[string]any{"http.response.status_code": "abc", "http.request.server.duration": "slow", "url.path": int64(7)})
	lr, _ := gcplbRun(t, defaultConfig(), input{attrs: attrs, resource: gcplbResource()})
	if lr.Body().Str() == "" {
		t.Fatal("body empty")
	}
}

func TestConsume_GCPLB_NoLogSource_Unmatched(t *testing.T) {
	h := newHarness(t, defaultConfig())
	lr, res := h.run(input{attrs: gcplbAttrs(map[string]any{"log.source": nil}), resource: gcplbResource()})
	if lr.Body().Str() != "" || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: %q %v", lr.Body().Str(), lr.SeverityNumber())
	}
	absent(t, res.Attributes(), "host.name", "service.name")
}

func TestConsume_GCPLB_OtherSource_Unmatched(t *testing.T) {
	lr, res := gcplbRun(t, defaultConfig(), input{attrs: gcplbAttrs(map[string]any{"log.source": "gcp.cloudrun"}), resource: gcplbResource()})
	if lr.Body().Str() != "" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if _, ok := res["host.name"]; ok {
		t.Fatalf("resource changed: %v", res)
	}
}

type lbOut struct {
	grp int
	res map[string]any
	lr  plog.LogRecord
}

// lbBatch sends several records under one shared resource in one batch and returns
// the records in output order (grouped by resource), each with the resource group it left under.
func lbBatch(t *testing.T, cfg *Config, resource map[string]any, recs []map[string]any) []lbOut {
	t.Helper()
	h := newHarness(t, cfg)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(resource); err != nil {
		t.Fatal(err)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for _, a := range recs {
		if err := sl.LogRecords().AppendEmpty().Attributes().FromRaw(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}
	var outs []lbOut
	grp := 0
	for _, l := range h.sink.AllLogs() {
		for i := range l.ResourceLogs().Len() {
			o := l.ResourceLogs().At(i)
			grp++
			for j := range o.ScopeLogs().Len() {
				for k := range o.ScopeLogs().At(j).LogRecords().Len() {
					outs = append(outs, lbOut{grp: grp, res: o.Resource().Attributes().AsRaw(), lr: o.ScopeLogs().At(j).LogRecords().At(k)})
				}
			}
		}
	}
	return outs
}

func TestConsume_GCPLB_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	cfg := gcplbOnly(nil)
	cfg.AccountAliases = map[string]string{"proj-one": "alias-one"}
	rec := func(project, backend, path string) map[string]any {
		return gcplbAttrs(map[string]any{"gcp.label.project_id": project, "gcp.label.backend_service_name": backend, "url.path": path})
	}
	outs := lbBatch(t, cfg, nil, []map[string]any{
		rec("proj-one", "k8s1-0a1b2c3d-monitoring-grafana-3000-4e5f6a7b", "/a"),
		rec("proj-two", "k8s1-0a1b2c3d-monitoring-prom-9090-4e5f6a7b", "/b"), // pragma: allowlist secret
		rec("proj-one", "k8s1-0a1b2c3d-monitoring-grafana-3000-4e5f6a7b", "/c"),
	})
	want := []struct{ host, svc, path string }{
		{"alias-one", "monitoring-grafana-3000", "/a"},
		{"alias-one", "monitoring-grafana-3000", "/c"},
		{"proj-two", "monitoring-prom-9090", "/b"},
	}
	if len(outs) != 3 || outs[0].grp != outs[1].grp || outs[1].grp == outs[2].grp {
		t.Fatalf("grouping wrong: %v", outs)
	}
	for i, w := range want {
		if outs[i].res["host.name"] != w.host || outs[i].res["service.name"] != w.svc {
			t.Fatalf("record %d resource = %v, want host %s service %s", i, outs[i].res, w.host, w.svc)
		}
		if got := attrStr(t, outs[i].lr.Attributes(), "url.path"); got != w.path {
			t.Fatalf("record %d path = %s, want %s", i, got, w.path)
		}
	}
}

func TestConsume_GCPLB_AliasedProject_HostIsAliasAndAccountNameSet(t *testing.T) {
	cfg := gcplbOnly(nil)
	cfg.AccountAliases = map[string]string{"example-project": "test-ops"}
	_, res := gcplbRun(t, cfg, input{attrs: gcplbAttrs(nil), resource: gcplbResource()})
	if res["host.name"] != "test-ops" || res["cloud.account.name"] != "test-ops" {
		t.Fatalf("resource = %v", res)
	}
}

func TestConsume_GCPLB_UnmappedProject_HostAndAccountNameAreTheRawID(t *testing.T) {
	cfg := gcplbOnly(nil)
	cfg.AccountAliases = map[string]string{"another-project": "test-ops"}
	_, res := gcplbRun(t, cfg, input{attrs: gcplbAttrs(nil), resource: gcplbResource()})
	if res["host.name"] != "example-project" || res["cloud.account.name"] != "example-project" {
		t.Fatalf("resource = %v", res)
	}
}

func TestConsume_GCPLB_NoProjectAttribute_NoHostNoAccountName(t *testing.T) {
	cfg := gcplbOnly(nil)
	cfg.AccountAliases = map[string]string{"example-project": "test-ops"}
	_, res := gcplbRun(t, cfg, input{attrs: gcplbAttrs(nil)})
	absent(t, mapOf(t, res), "host.name", "cloud.account.name")
}

func TestConsume_GCPLB_ExistingOriginal_Kept(t *testing.T) {
	attrs := gcplbAttrs(map[string]any{"log.record.original": "first original"})
	lr, _ := gcplbRun(t, gcplbOnly(nil), input{body: "raw lb line", attrs: attrs, resource: gcplbResource()})
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "first original" {
		t.Fatalf("log.record.original = %q", got)
	}
}

func mapOf(t *testing.T, raw map[string]any) pcommon.Map {
	t.Helper()
	m := pcommon.NewMap()
	if err := m.FromRaw(raw); err != nil {
		t.Fatal(err)
	}
	return m
}
