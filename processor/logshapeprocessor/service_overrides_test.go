// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// fakeServiceBranch sets service.name on the resource copy, or fails.
type fakeServiceBranch struct {
	service string
	err     error
}

func (*fakeServiceBranch) name() string     { return "fake" }
func (*fakeServiceBranch) claims(*rec) bool { return true }
func (f *fakeServiceBranch) parse(r *rec) error {
	if f.err != nil {
		return f.err
	}
	r.res.Attributes().PutStr("service.name", f.service)
	return nil
}

func overrideRec() *rec {
	return &rec{lr: plog.NewLogRecord(), res: pcommon.NewResource(), scope: pcommon.NewInstrumentationScope()}
}

func TestWithServiceOverrides_EmptyMap_ReturnsBranchUnwrapped(t *testing.T) {
	b := &fakeServiceBranch{service: "a"}

	if withServiceOverrides(b, nil) != branch(b) || withServiceOverrides(b, map[string]string{}) != branch(b) {
		t.Fatal("nil or empty overrides must return the branch itself")
	}
}

func TestWithServiceOverrides_Listed_ReplacesName(t *testing.T) {
	w := withServiceOverrides(&fakeServiceBranch{service: "a"}, map[string]string{"a": "b"})
	r := overrideRec()

	if err := w.parse(r); err != nil {
		t.Fatal(err)
	}

	if got := attrStr(t, r.res.Attributes(), "service.name"); got != "b" {
		t.Fatalf("service.name = %q, want b", got)
	}
	if w.name() != "fake" || !w.claims(r) {
		t.Fatal("name and claims must delegate")
	}
}

func TestWithServiceOverrides_Unlisted_Untouched(t *testing.T) {
	w := withServiceOverrides(&fakeServiceBranch{service: "c"}, map[string]string{"a": "b"})
	r := overrideRec()

	if err := w.parse(r); err != nil {
		t.Fatal(err)
	}

	if got := attrStr(t, r.res.Attributes(), "service.name"); got != "c" {
		t.Fatalf("service.name = %q, want c", got)
	}
}

func TestWithServiceOverrides_NoServiceName_StaysAbsent(t *testing.T) {
	w := withServiceOverrides(&fakeServiceBranch{}, map[string]string{"": "b"})
	r := overrideRec()
	_ = w.parse(r)

	if v, ok := r.res.Attributes().Get("service.name"); ok && v.Str() == "b" {
		t.Fatal("an empty derived name must not be overridden")
	}
}

func TestWithServiceOverrides_ParseError_PassedThrough(t *testing.T) {
	boom := errors.New("boom")
	w := withServiceOverrides(&fakeServiceBranch{err: boom}, map[string]string{"a": "b"})

	if err := w.parse(overrideRec()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func overrideTrailInput(session, eventID string) input {
	in := input{
		body: "ssm.amazonaws.com GetParameter",
		attrs: map[string]any{
			"log.source":              "aws.cloudtrail",
			"event.category":          "audit",
			"rpc.method":              "ssm.amazonaws.com/GetParameter",
			"aws.principal.arn":       "arn:aws:sts::111122223333:assumed-role/TestRole/" + session,
			"aws.principal.type":      "AssumedRole",
			"aws.cloudtrail.event_id": eventID,
		},
		resource: map[string]any{"cloud.account.id": "111122223333", "cloud.provider": "aws"},
	}
	return in
}

func TestConsume_ServiceOverrides_ListedName_Replaced(t *testing.T) {
	cfg := defaultConfig()
	cfg.ServiceNameOverrides = map[string]string{"test-session": "ops-team"}
	h := newHarness(t, cfg)

	_, res := h.run(overrideTrailInput("test-session", "e1"))

	if got := attrStr(t, res.Attributes(), "service.name"); got != "ops-team" {
		t.Fatalf("service.name = %q, want ops-team", got)
	}
}

func TestConsume_ServiceOverrides_UnlistedName_Untouched(t *testing.T) {
	cfg := defaultConfig()
	cfg.ServiceNameOverrides = map[string]string{"other": "ops-team"}
	h := newHarness(t, cfg)

	_, res := h.run(overrideTrailInput("test-session", "e1"))

	if got := attrStr(t, res.Attributes(), "service.name"); got != "test-session" {
		t.Fatalf("service.name = %q, want test-session", got)
	}
}

func TestConsume_ServiceOverrides_NilMap_NoOp(t *testing.T) {
	h := newHarness(t, defaultConfig())

	_, res := h.run(overrideTrailInput("test-session", "e1"))

	if got := attrStr(t, res.Attributes(), "service.name"); got != "test-session" {
		t.Fatalf("service.name = %q, want test-session", got)
	}
}

func TestConsume_ServiceOverrides_MixedBatch_PerRecord(t *testing.T) {
	cfg := defaultConfig()
	cfg.ServiceNameOverrides = map[string]string{"test-session": "ops-team"}
	h := newHarness(t, cfg)
	ld := overrideTrailInput("test-session", "e1").logs()
	second := overrideTrailInput("other-session", "e2").logs()
	second.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).CopyTo(ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().AppendEmpty())

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	out := h.sink.AllLogs()[0]
	for i := range out.ResourceLogs().Len() {
		rl := out.ResourceLogs().At(i)
		svc := attrStr(t, rl.Resource().Attributes(), "service.name")
		for j := range rl.ScopeLogs().Len() {
			lrs := rl.ScopeLogs().At(j).LogRecords()
			for k := range lrs.Len() {
				got[attrStr(t, lrs.At(k).Attributes(), "aws.cloudtrail.event_id")] = svc
			}
		}
	}
	if len(got) != 2 || got["e1"] != "ops-team" || got["e2"] != "other-session" {
		t.Fatalf("per-record services = %v", got)
	}
}
