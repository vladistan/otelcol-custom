// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore awselb

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func awselbAttrs(over map[string]any) map[string]any {
	a := map[string]any{
		"log.source":                     "aws.elb",
		"http.request.method":            "GET",
		"url.domain":                     "os.app.example.invalid",
		"url.full":                       "https://os.app.example.invalid:443/api/v1/bus/status?x=1",
		"aws.elb.status.code":            int64(200),
		"aws.elb.target_processing_time": 0.011,
		"aws.elb.target_group_arn":       "arn:aws:elasticloadbalancing:eu-west-1:111122223333:targetgroup/os-app-tg/0123456789abcdef",
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

func awselbResource() map[string]any {
	return map[string]any{
		"cloud.resource_id": "app/example-lb/50dc6c495c0c9188",
		"cloud.account.id":  "111122223333",
	}
}

func awselbConfig() *Config {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"111122223333": "test-ops"}
	return cfg
}

func awselbRun(t *testing.T, cfg *Config, in input) (plog.LogRecord, map[string]any) {
	t.Helper()
	h := newHarness(t, cfg)
	lr, res := h.run(in)
	return lr, res.Attributes().AsRaw()
}

func awselbSeverity(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %v %q, want %v %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func TestConsume_AWSELB_FullRecord_Shaped(t *testing.T) {
	lr, res := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(nil), resource: awselbResource()})
	if got := lr.Body().Str(); got != "GET os.app.example.invalid/api/v1/bus/status 200 11ms" {
		t.Fatalf("body = %q", got)
	}
	want := map[string]any{
		"host.name":          "example-lb",
		"service.name":       "os-app-tg",
		"cloud.account.id":   "111122223333",
		"cloud.account.name": "test-ops",
		"cloud.resource_id":  "app/example-lb/50dc6c495c0c9188",
	}
	for k, v := range want {
		if res[k] != v {
			t.Fatalf("resource[%s] = %v, want %v; have %v", k, res[k], v, res)
		}
	}
	awselbSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	attrInt(t, lr.Attributes(), "aws.elb.status.code", 200)
	if got := attrStr(t, lr.Attributes(), "log.source"); got != "aws.elb" {
		t.Fatalf("log.source = %q", got)
	}
}

func TestConsume_AWSELB_UnlistedAccount_NoAccountName(t *testing.T) {
	res := map[string]any{"cloud.resource_id": "app/example-lb/50dc6c495c0c9188", "cloud.account.id": "444455556666"}
	_, got := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(nil), resource: res})
	if _, ok := got["cloud.account.name"]; ok {
		t.Fatalf("alias set for an unlisted account: %v", got)
	}
}

func TestConsume_AWSELB_NoAliasConfig_NoAccountName(t *testing.T) {
	_, got := awselbRun(t, defaultConfig(), input{attrs: awselbAttrs(nil), resource: awselbResource()})
	if _, ok := got["cloud.account.name"]; ok {
		t.Fatalf("alias set without config: %v", got)
	}
}

func TestConsume_AWSELB_StatusClasses_Severity(t *testing.T) {
	cases := []struct {
		status int64
		num    plog.SeverityNumber
		text   string
	}{
		{301, plog.SeverityNumberInfo, "INFO"},
		{403, plog.SeverityNumberWarn, "WARN"},
		{504, plog.SeverityNumberError, "ERROR"},
	}
	for _, c := range cases {
		lr, _ := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(map[string]any{"aws.elb.status.code": c.status}), resource: awselbResource()})
		awselbSeverity(t, lr, c.num, c.text)
	}
}

func TestConsume_AWSELB_NoStatus_SeverityUnsetBodyKept(t *testing.T) {
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(map[string]any{"aws.elb.status.code": nil}), resource: awselbResource()})
	awselbSeverity(t, lr, plog.SeverityNumberUnspecified, "")
	if got := lr.Body().Str(); got != "GET os.app.example.invalid/api/v1/bus/status 11ms" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_AWSELB_StandardStatusName_Used(t *testing.T) {
	attrs := awselbAttrs(map[string]any{"aws.elb.status.code": nil, "http.response.status_code": int64(404)})
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: attrs, resource: awselbResource()})
	awselbSeverity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestConsume_AWSELB_PathFromFullURL_Body(t *testing.T) {
	attrs := awselbAttrs(map[string]any{"url.domain": nil, "url.full": "https://os.app.example.invalid:443/api/v1/x?y=1"})
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: attrs, resource: awselbResource()})
	if got := lr.Body().Str(); got != "GET os.app.example.invalid/api/v1/x 200 11ms" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_AWSELB_NoTargetTime_NoDuration(t *testing.T) {
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(map[string]any{"aws.elb.target_processing_time": -1.0}), resource: awselbResource()})
	if got := lr.Body().Str(); got != "GET os.app.example.invalid/api/v1/bus/status 200" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_AWSELB_ExistingBodyNothingToBuild_Kept(t *testing.T) {
	attrs := map[string]any{"log.source": "aws.elb"}
	lr, _ := awselbRun(t, awselbConfig(), input{body: "raw elb line", attrs: attrs})
	if got := lr.Body().Str(); got != "raw elb line" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_AWSELB_NoResourceFields_NoHostOrService(t *testing.T) {
	_, res := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(map[string]any{"aws.elb.target_group_arn": nil})})
	absent2 := []string{"host.name", "service.name", "cloud.account.name"}
	for _, k := range absent2 {
		if _, ok := res[k]; ok {
			t.Fatalf("%s present: %v", k, res)
		}
	}
}

func TestConsume_AWSELB_WrongTypedAttributes_NoPanic(t *testing.T) {
	attrs := awselbAttrs(map[string]any{"aws.elb.status.code": "x", "aws.elb.target_processing_time": "y", "aws.elb.target_group_arn": int64(3), "url.full": true})
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: attrs, resource: awselbResource()})
	if lr.Body().Str() == "" {
		t.Fatal("body empty")
	}
}

func TestConsume_AWSELB_OtherSource_Unmatched(t *testing.T) {
	lr, res := awselbRun(t, awselbConfig(), input{attrs: awselbAttrs(map[string]any{"log.source": "aws.alb"}), resource: awselbResource()})
	if lr.Body().Str() != "" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if _, ok := res["host.name"]; ok {
		t.Fatalf("resource changed: %v", res)
	}
}

func TestConsume_AWSELB_UrlPathAttribute_UsedWhenNoFullURL(t *testing.T) {
	attrs := awselbAttrs(map[string]any{"url.full": nil, "url.path": "/api/v1/p"})
	lr, _ := awselbRun(t, awselbConfig(), input{attrs: attrs, resource: awselbResource()})
	if got := lr.Body().Str(); got != "GET os.app.example.invalid/api/v1/p 200 11ms" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_AWSELB_ExistingOriginal_Kept(t *testing.T) {
	attrs := awselbAttrs(map[string]any{"log.record.original": "first original"})
	lr, _ := awselbRun(t, awselbConfig(), input{body: "raw elb line", attrs: attrs, resource: awselbResource()})
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "first original" {
		t.Fatalf("log.record.original = %q", got)
	}
}

func TestConsume_AWSELB_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	rec := func(lb, tg, path string) map[string]any {
		return awselbAttrs(map[string]any{
			"cloud.resource_id":        "app/" + lb + "/aaaa",
			"aws.elb.target_group_arn": "arn:aws:elasticloadbalancing:eu-west-1:111122223333:targetgroup/" + tg + "/0123456789abcdef",
			"url.domain":               nil,
			"url.full":                 "https://a.example.invalid" + path,
		})
	}
	outs := lbBatch(t, awselbConfig(), map[string]any{"cloud.account.id": "111122223333"}, []map[string]any{
		rec("lb-one", "tg-one", "/one"), rec("lb-two", "tg-two", "/two"), rec("lb-one", "tg-one", "/three"),
	})
	want := []struct{ host, svc, body string }{
		{"lb-one", "tg-one", "GET a.example.invalid/one 200 11ms"},
		{"lb-one", "tg-one", "GET a.example.invalid/three 200 11ms"},
		{"lb-two", "tg-two", "GET a.example.invalid/two 200 11ms"},
	}
	if len(outs) != 3 || outs[0].grp != outs[1].grp || outs[1].grp == outs[2].grp {
		t.Fatalf("grouping wrong: %v", outs)
	}
	for i, w := range want {
		if outs[i].res["host.name"] != w.host || outs[i].res["service.name"] != w.svc || outs[i].res["cloud.account.name"] != "test-ops" {
			t.Fatalf("record %d resource = %v, want host %s service %s", i, outs[i].res, w.host, w.svc)
		}
		if got := outs[i].lr.Body().Str(); got != w.body {
			t.Fatalf("record %d body = %q, want %q", i, got, w.body)
		}
	}
}
