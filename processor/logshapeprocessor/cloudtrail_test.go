// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const cloudTrailTestAccount = "111122223333"

func cloudTrailConfig() *Config {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{cloudTrailTestAccount: "test-ops"}
	return cfg
}

func cloudTrailInput(attrs map[string]any) input {
	a := map[string]any{"log.source": "aws.cloudtrail", "event.category": "audit"}
	for k, v := range attrs {
		a[k] = v
	}
	return input{
		body:     "ssm.amazonaws.com GetParameter",
		attrs:    a,
		resource: map[string]any{"cloud.account.id": cloudTrailTestAccount, "cloud.provider": "aws"},
	}
}

func cloudTrailRoleAttrs() map[string]any {
	return map[string]any{
		"rpc.method":              "ssm.amazonaws.com/GetParameter",
		"aws.principal.arn":       "arn:aws:sts::111122223333:assumed-role/TestRole/test-session",
		"aws.principal.type":      "AssumedRole",
		"aws.request.parameters":  map[string]any{"name": "/instance-access/mappings/general"},
		"aws.cloudtrail.event_id": "00000000-0000-0000-0000-000000000001",
	}
}

func TestConsume_CloudTrail_AssumedRoleRecord_ShapesEveryField(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())

	lr, res := h.run(cloudTrailInput(cloudTrailRoleAttrs()))

	if got := lr.Body().Str(); got != "ssm:GetParameter /instance-access/mappings/general" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Fatalf("severity = %d %q, want unset", lr.SeverityNumber(), lr.SeverityText())
	}
	r := res.Attributes()
	if attrStr(t, r, "service.name") != "test-session" || attrStr(t, r, "host.name") != "test-ops" ||
		attrStr(t, r, "cloud.account.name") != "test-ops" || attrStr(t, r, "cloud.account.id") != cloudTrailTestAccount {
		t.Fatalf("resource = %v", r.AsRaw())
	}
	a := lr.Attributes()
	if attrStr(t, a, "log.source") != "aws.cloudtrail" || attrStr(t, a, "rpc.method") != "ssm.amazonaws.com/GetParameter" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestConsume_CloudTrail_AssumeRoleEvent_TargetIsRoleName(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := map[string]any{
		"rpc.method":             "sts.amazonaws.com/AssumeRole",
		"aws.request.parameters": map[string]any{"roleArn": "arn:aws:iam::111122223333:role/aws-service-role/ipam.amazonaws.com/AWSServiceRoleForIPAM", "roleSessionName": "x"},
	}

	lr, _ := h.run(cloudTrailInput(attrs))

	if lr.Body().Str() != "sts:AssumeRole AWSServiceRoleForIPAM" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_CloudTrail_NoRequestParameters_BodyIsActionOnly(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())

	lr, _ := h.run(cloudTrailInput(map[string]any{"rpc.method": "ec2.amazonaws.com/DescribeInstances"}))

	if lr.Body().Str() != "ec2:DescribeInstances" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_CloudTrail_BareEventNameWithRPCService_BuildsAction(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())

	lr, _ := h.run(cloudTrailInput(map[string]any{"rpc.method": "GetObject", "rpc.service": "s3.amazonaws.com"}))
	if lr.Body().Str() != "s3:GetObject" {
		t.Fatalf("body = %q", lr.Body().Str())
	}

	lr, _ = h.run(cloudTrailInput(map[string]any{"rpc.method": "GetObject"}))
	if lr.Body().Str() != "GetObject" {
		t.Fatalf("no source: body = %q", lr.Body().Str())
	}
}

func TestConsume_CloudTrail_ErrorCodes_StayWarn(t *testing.T) {
	for _, code := range []string{"AccessDenied", "NoSuchKey", "ThrottlingException"} {
		h := newHarness(t, cloudTrailConfig())
		attrs := cloudTrailRoleAttrs()
		attrs["aws.error.code"] = code

		lr, _ := h.run(cloudTrailInput(attrs))

		if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
			t.Errorf("%s: severity = %d %q", code, lr.SeverityNumber(), lr.SeverityText())
		}
	}
}

func TestConsume_CloudTrail_EmptyErrorCode_StaysUnset(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := cloudTrailRoleAttrs()
	attrs["aws.error.code"] = ""

	lr, _ := h.run(cloudTrailInput(attrs))

	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("severity = %d", lr.SeverityNumber())
	}
}

func TestConsume_CloudTrail_PrincipalShortening_Cases(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		{"assumed role gives the session name", map[string]any{"aws.principal.arn": "arn:aws:sts::111122223333:assumed-role/TestRole/ssh-key-agent"}, "ssh-key-agent"},
		{"prefixed arn name is read", map[string]any{"aws.user_identity.principal.arn": "arn:aws:iam::111122223333:user/alice"}, "alice"},
		{"iam user name without arn", map[string]any{"user.name": "bob"}, "bob"},
		{"aws service invoker", map[string]any{"aws.user_identity.invoked_by": "ipam.amazonaws.com"}, "AWS Internal"},
		{"legacy invoker name", map[string]any{"aws.user_identity.invoked_by": "cloudformation.amazonaws.com"}, "AWS Internal"},
		{"over-long session name is cut", map[string]any{"aws.principal.arn": "arn:aws:sts::111122223333:assumed-role/R/a-very-long-session-name-that-keeps-going"}, "a-very-long-session-name-that-ke"},
	}
	for _, c := range cases {
		h := newHarness(t, cloudTrailConfig())
		_, res := h.run(cloudTrailInput(c.attrs))
		if got := attrStr(t, res.Attributes(), "service.name"); got != c.want {
			t.Errorf("%s: service.name = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestConsume_CloudTrail_UnlistedAccount_HostIsIDAndNoAccountName(t *testing.T) {
	h := newHarness(t, defaultConfig())

	_, res := h.run(cloudTrailInput(cloudTrailRoleAttrs()))

	if attrStr(t, res.Attributes(), "host.name") != cloudTrailTestAccount {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
	absent(t, res.Attributes(), "cloud.account.name")
}

func TestConsume_CloudTrail_AccountIDOnRecord_IsUsed(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	in := cloudTrailInput(cloudTrailRoleAttrs())
	in.resource = map[string]any{}
	in.attrs["cloud.account.id"] = cloudTrailTestAccount

	_, res := h.run(in)

	if attrStr(t, res.Attributes(), "host.name") != "test-ops" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestConsume_CloudTrail_NoAccountID_NoHost(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	in := cloudTrailInput(cloudTrailRoleAttrs())
	in.resource = map[string]any{}

	_, res := h.run(in)

	absent(t, res.Attributes(), "host.name", "cloud.account.name")
}

func TestConsume_CloudTrail_Malformed_DoesNotPanic(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := map[string]any{
		"rpc.method":             int64(5),
		"aws.principal.arn":      []any{"x"},
		"aws.request.parameters": "not a map",
		"aws.error.code":         int64(1),
	}
	in := cloudTrailInput(attrs)
	in.resource = map[string]any{"cloud.account.id": int64(3)}

	lr, res := h.run(in)

	if lr.Body().Str() != "ssm.amazonaws.com GetParameter" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	absent(t, res.Attributes(), "host.name")
}

func TestConsume_CloudTrail_EmptyValues_DoNotPanic(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := map[string]any{
		"rpc.method":             "/",
		"aws.principal.arn":      "",
		"aws.request.parameters": map[string]any{"name": "", "roleArn": 7},
	}

	lr, _ := h.run(cloudTrailInput(attrs))

	if lr.Body().Str() == "" {
		t.Fatal("body emptied")
	}
}

func TestConsume_CloudTrail_OtherLogSource_PassesUnmatched(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	in := cloudTrailInput(cloudTrailRoleAttrs())
	in.attrs["log.source"] = "aws.cloudwatch"

	lr, res := h.run(in)

	if lr.Body().Str() != "ssm.amazonaws.com GetParameter" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	absent(t, res.Attributes(), "host.name", "service.name")
}

func TestConsume_CloudTrail_Batch_CountInEqualsOut(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	_ = rl.Resource().Attributes().FromRaw(map[string]any{"cloud.account.id": cloudTrailTestAccount})
	lrs := rl.ScopeLogs().AppendEmpty().LogRecords()
	const n = 30
	for i := range n {
		lr := lrs.AppendEmpty()
		lr.Body().SetStr("raw")
		attrs := cloudTrailRoleAttrs()
		attrs["log.source"] = "aws.cloudtrail"
		if i%3 == 0 {
			attrs["aws.error.code"] = "AccessDenied"
		}
		_ = lr.Attributes().FromRaw(attrs)
	}

	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}

	out := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	if out.Len() != n {
		t.Fatalf("records out = %d, want %d", out.Len(), n)
	}
	for i := range out.Len() {
		if out.At(i).Body().Str() != "ssm:GetParameter /instance-access/mappings/general" {
			t.Fatalf("record %d body = %q", i, out.At(i).Body().Str())
		}
	}
}

func TestConsume_CloudTrail_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"111122223333": "alias-a", "444455556666": "alias-b"}
	h := newHarness(t, cfg)
	rec := func(body, arn, account string) auditRec {
		a := cloudTrailRoleAttrs()
		a["log.source"] = "aws.cloudtrail"
		a["aws.principal.arn"] = arn
		a["cloud.account.id"] = account
		return auditRec{body: body, attrs: a}
	}

	out := h.auditBatch(map[string]any{"cloud.provider": "aws"}, []auditRec{
		rec("one", "arn:aws:sts::111122223333:assumed-role/R/alice-session", "111122223333"),
		rec("two", "arn:aws:sts::444455556666:assumed-role/R/bob-session", "444455556666"),
		rec("three", "arn:aws:sts::111122223333:assumed-role/R/carol-session", "111122223333"),
	})

	wants := []struct{ service, host string }{
		{"alice-session", "alias-a"}, {"bob-session", "alias-b"}, {"carol-session", "alias-a"},
	}
	if len(out) != len(wants) {
		t.Fatalf("records out = %d", len(out))
	}
	for i, w := range wants {
		r := out[i].res.Attributes()
		if attrStr(t, r, "service.name") != w.service || attrStr(t, r, "host.name") != w.host || attrStr(t, r, "cloud.account.name") != w.host {
			t.Errorf("record %d: resource %v, want %+v", i, r.AsRaw(), w)
		}
	}
}

func TestConsume_CloudTrail_SecondPass_ChangesNothing(t *testing.T) {
	failing := cloudTrailRoleAttrs()
	failing["log.source"] = "aws.cloudtrail"
	failing["aws.error.code"] = "AccessDenied"
	plain := cloudTrailRoleAttrs()
	plain["log.source"] = "aws.cloudtrail"
	bare := map[string]any{"log.source": "aws.cloudtrail", "rpc.method": "GetObject"}
	recs := []auditRec{{body: "raw", attrs: failing}, {body: "raw", attrs: plain}, {body: "raw", attrs: bare}}

	assertTwoPassesEqual(t, cloudTrailConfig(), map[string]any{"cloud.account.id": cloudTrailTestAccount}, recs)
}
