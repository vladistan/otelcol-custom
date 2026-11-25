// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
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
	a := map[string]any{"log.source": "cloudtrail", "event.category": "audit"}
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
	if attrStr(t, r, "service.name") != "TestRole/test-session" || attrStr(t, r, "host.name") != "test-ops" ||
		attrStr(t, r, "cloud.account.name") != "test-ops" || attrStr(t, r, "cloud.account.id") != cloudTrailTestAccount {
		t.Fatalf("resource = %v", r.AsRaw())
	}
	a := lr.Attributes()
	if attrStr(t, a, "log.source") != "cloudtrail" || attrStr(t, a, "rpc.method") != "ssm.amazonaws.com/GetParameter" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestConsume_CloudTrail_AssumeRoleEvent_BodyHasRoleAndSession(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := map[string]any{
		"rpc.method":             "sts.amazonaws.com/AssumeRole",
		"aws.request.parameters": map[string]any{"roleArn": "arn:aws:iam::111122223333:role/aws-service-role/ipam.amazonaws.com/AWSServiceRoleForIPAM", "roleSessionName": "x"},
	}

	lr, _ := h.run(cloudTrailInput(attrs))

	if lr.Body().Str() != "sts:AssumeRole AWSServiceRoleForIPAM x" {
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

func TestConsume_CloudTrail_ErrorCodes_AccessDeniedIsErrorOthersWarn(t *testing.T) {
	cases := []struct {
		code string
		num  plog.SeverityNumber
		text string
	}{
		{"AccessDenied", plog.SeverityNumberError, "ERROR"},
		{"NoSuchKey", plog.SeverityNumberWarn, "WARN"},
		{"ThrottlingException", plog.SeverityNumberWarn, "WARN"},
	}
	for _, c := range cases {
		h := newHarness(t, cloudTrailConfig())
		attrs := cloudTrailRoleAttrs()
		attrs["aws.error.code"] = c.code

		lr, _ := h.run(cloudTrailInput(attrs))

		if lr.SeverityNumber() != c.num || lr.SeverityText() != c.text {
			t.Errorf("%s: severity = %d %q", c.code, lr.SeverityNumber(), lr.SeverityText())
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

func TestConsume_CloudTrail_CallerByIdentityType_Cases(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		{"assumed role gives role and session", map[string]any{"aws.principal.type": "AssumedRole", "aws.principal.arn": "arn:aws:sts::111122223333:assumed-role/TestRole/ssh-key-agent"}, "TestRole/ssh-key-agent"},
		{"assumed role prefers the issuer name", map[string]any{"aws.principal.type": "AssumedRole", "aws.principal.arn": "arn:aws:sts::111122223333:assumed-role/Short/s1", "aws.user_identity.session_context.issuer.user_name": "IssuerRole"}, "IssuerRole/s1"},
		{"iam user name", map[string]any{"aws.principal.type": "IAMUser", "user.name": "bob"}, "bob"},
		{"saml user name", map[string]any{"aws.principal.type": "SAMLUser", "user.name": "carol"}, "carol"},
		{"web identity user name", map[string]any{"aws.principal.type": "WebIdentityUser", "user.name": "dave"}, "dave"},
		{"account gives the alias", map[string]any{"aws.principal.type": "AWSAccount", "aws.user_identity.account_id": cloudTrailTestAccount}, "test-ops"},
		{"account without alias gives the id", map[string]any{"aws.principal.type": "AWSAccount", "aws.user_identity.account_id": "999988887777"}, "999988887777"},
		{"aws service gives the service", map[string]any{"aws.principal.type": "AWSService", "aws.user_identity.invoked_by": "ssm.amazonaws.com"}, "ssm"},
		{"aws service keeps AWS Internal text", map[string]any{"aws.principal.type": "AWSService", "aws.user_identity.invoked_by": "AWS Internal"}, "AWS Internal"},
		{"no type gives cloudtrail", map[string]any{"aws.principal.arn": "arn:aws:iam::111122223333:user/alice"}, "cloudtrail"},
		{"unknown type gives cloudtrail", map[string]any{"aws.principal.type": "Root", "user.name": "root"}, "cloudtrail"},
		{"over-long role and session are cut", map[string]any{"aws.principal.type": "AssumedRole", "aws.principal.arn": "arn:aws:sts::111122223333:assumed-role/R/a-very-long-session-name-that-keeps-going"}, "R/a-very-long-session-name-that-"},
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
		attrs["log.source"] = "cloudtrail"
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
		a["log.source"] = "cloudtrail"
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
		{"R/alice-session", "alias-a"}, {"R/bob-session", "alias-b"}, {"R/carol-session", "alias-a"},
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
	failing["log.source"] = "cloudtrail"
	failing["aws.error.code"] = "AccessDenied"
	plain := cloudTrailRoleAttrs()
	plain["log.source"] = "cloudtrail"
	bare := map[string]any{"log.source": "cloudtrail", "rpc.method": "GetObject"}
	recs := []auditRec{{body: "raw", attrs: failing}, {body: "raw", attrs: plain}, {body: "raw", attrs: bare}}

	assertTwoPassesEqual(t, cloudTrailConfig(), map[string]any{"cloud.account.id": cloudTrailTestAccount}, recs)
}

func assumeRoleAttrs(params, response map[string]any) map[string]any {
	a := map[string]any{
		"rpc.method":             "sts.amazonaws.com/AssumeRole",
		"aws.request.parameters": params,
	}
	if response != nil {
		a["aws.response.elements"] = response
	}
	return a
}

func TestConsume_CloudTrail_AssumeRole_SessionFromResponseWhenRequestHasNone(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	params := map[string]any{"roleArn": "arn:aws:iam::111122223333:role/NodeRole"}
	resp := map[string]any{"assumedRoleUser": map[string]any{"arn": "arn:aws:sts::111122223333:assumed-role/NodeRole/i-0123456789abcdef0"}}

	lr, _ := h.run(cloudTrailInput(assumeRoleAttrs(params, resp)))

	if lr.Body().Str() != "sts:AssumeRole NodeRole i-0123456789abcdef0" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_CloudTrail_AssumeRole_NoSession_BodyEndsAtRole(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	params := map[string]any{"roleArn": "arn:aws:iam::111122223333:role/NodeRole"}

	lr, _ := h.run(cloudTrailInput(assumeRoleAttrs(params, nil)))

	if lr.Body().Str() != "sts:AssumeRole NodeRole" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func s3Attrs(params map[string]any) map[string]any {
	return map[string]any{"rpc.method": "s3.amazonaws.com/GetObject", "aws.request.parameters": params}
}

func TestConsume_CloudTrail_S3GetObject_BodyHasBucketAndKey(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())

	lr, _ := h.run(cloudTrailInput(s3Attrs(map[string]any{"bucketName": "test-bucket", "key": "logs/2026/a.txt"})))

	if lr.Body().Str() != "s3:GetObject test-bucket/logs/2026/a.txt" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func TestConsume_CloudTrail_S3GetObject_LongKeyIsCutInTheMiddle(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	key := "prefix/" + strings.Repeat("0123456789", 12) + "/tail-end.bin"

	lr, _ := h.run(cloudTrailInput(s3Attrs(map[string]any{"bucketName": "test-bucket", "key": key})))

	got := strings.TrimPrefix(lr.Body().Str(), "s3:GetObject test-bucket/")
	if !strings.Contains(got, "..") || !strings.HasPrefix(got, "prefix/0123") || !strings.HasSuffix(got, "tail-end.bin") || len(got) > 41 {
		t.Fatalf("key part = %q", got)
	}
}

func TestConsume_CloudTrail_S3GetObject_NoKey_BodyHasBucketOnly(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())

	lr, _ := h.run(cloudTrailInput(s3Attrs(map[string]any{"bucketName": "test-bucket"})))

	if lr.Body().Str() != "s3:GetObject test-bucket" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
}

func credentialsResponse() map[string]any {
	return map[string]any{
		"credentials": map[string]any{
			"accessKeyId":  "ASIATESTKEY",
			"sessionToken": "test-token",
			"expiration":   "Jan 1, 2026, 1:00:00 AM",
		},
		"assumedRoleUser": map[string]any{"arn": "arn:aws:sts::111122223333:assumed-role/R/s"},
	}
}

func TestConsume_CloudTrail_AssumeRole_CredentialsAreRedacted(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	params := map[string]any{"roleArn": "arn:aws:iam::111122223333:role/R"}

	lr, _ := h.run(cloudTrailInput(assumeRoleAttrs(params, credentialsResponse())))

	v, _ := lr.Attributes().Get("aws.response.elements")
	creds := v.Map().AsRaw()["credentials"].(map[string]any)
	if creds["accessKeyId"] != "[REDACTED]" || creds["sessionToken"] != "[REDACTED]" {
		t.Fatalf("credentials = %v", creds)
	}
	if creds["expiration"] != "Jan 1, 2026, 1:00:00 AM" {
		t.Fatalf("expiration changed: %v", creds)
	}
}

func TestConsume_CloudTrail_AssumeRole_NoCredentialsBlock_RowUnchanged(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	params := map[string]any{"roleArn": "arn:aws:iam::111122223333:role/R"}
	resp := map[string]any{"other": "kept"}
	attrs := assumeRoleAttrs(params, resp)
	attrs["aws.error.code"] = "AccessDenied"

	lr, _ := h.run(cloudTrailInput(attrs))

	v, _ := lr.Attributes().Get("aws.response.elements")
	if got := v.Map().AsRaw(); len(got) != 1 || got["other"] != "kept" {
		t.Fatalf("elements = %v", got)
	}
}

func TestConsume_CloudTrail_OtherEvent_CredentialsNotTouched(t *testing.T) {
	h := newHarness(t, cloudTrailConfig())
	attrs := map[string]any{"rpc.method": "sts.amazonaws.com/GetSessionToken", "aws.response.elements": credentialsResponse()}

	lr, _ := h.run(cloudTrailInput(attrs))

	v, _ := lr.Attributes().Get("aws.response.elements")
	if v.Map().AsRaw()["credentials"].(map[string]any)["accessKeyId"] != "ASIATESTKEY" {
		t.Fatalf("elements = %v", v.Map().AsRaw())
	}
}
