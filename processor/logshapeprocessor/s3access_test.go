// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func s3accessAttrs(over map[string]any) map[string]any {
	a := map[string]any{
		"log.source":                "s3access",
		"rpc.method":                "BATCH.DELETE.OBJECT",
		"aws.s3.key":                ".chunks/ab/abcdef",
		"http.response.status_code": int64(204),
		"http.response.body.size":   int64(174 * 1024),
		"user.id":                   "arn:aws:iam::111122223333:user/backup-sync",
		"source.address":            "192.0.2.7",
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

func s3accessResource() map[string]any {
	return map[string]any{
		"aws.s3.bucket":    "bucket-name",
		"cloud.account.id": "111122223333",
	}
}

func s3accessConfig() *Config {
	cfg := defaultConfig()
	cfg.AccountAliases = map[string]string{"111122223333": "test-ops"}
	return cfg
}

func s3accessRun(t *testing.T, cfg *Config, in input) (plog.LogRecord, map[string]any) {
	t.Helper()
	h := newHarness(t, cfg)
	lr, res := h.run(in)
	return lr, res.Attributes().AsRaw()
}

func s3accessSeverity(t *testing.T, lr plog.LogRecord, num plog.SeverityNumber, text string) {
	t.Helper()
	if lr.SeverityNumber() != num || lr.SeverityText() != text {
		t.Fatalf("severity = %v %q, want %v %q", lr.SeverityNumber(), lr.SeverityText(), num, text)
	}
}

func TestConsume_S3Access_FullRecord_Shaped(t *testing.T) {
	lr, res := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(nil), resource: s3accessResource()})
	if got := lr.Body().Str(); got != "BATCH.DELETE.OBJECT bucket-name/.chunks/ab/abcdef 204 174KiB" {
		t.Fatalf("body = %q", got)
	}
	want := map[string]any{
		"host.name":          "bucket-name",
		"service.name":       "backup-sync",
		"cloud.account.id":   "111122223333",
		"cloud.account.name": "test-ops",
		"aws.s3.bucket":      "bucket-name",
	}
	for k, v := range want {
		if res[k] != v {
			t.Fatalf("resource[%s] = %v, want %v; have %v", k, res[k], v, res)
		}
	}
	s3accessSeverity(t, lr, plog.SeverityNumberInfo, "INFO")
	a := lr.Attributes()
	if got := attrStr(t, a, "client.address"); got != "192.0.2.7" {
		t.Fatalf("client.address = %q", got)
	}
	absent(t, a, "source.address")
	attrInt(t, a, "http.response.status_code", 204)
	if got := attrStr(t, a, "rpc.method"); got != "BATCH.DELETE.OBJECT" {
		t.Fatalf("rpc.method = %q", got)
	}
}

func TestConsume_S3Access_ClientAddressAlreadySet_Kept(t *testing.T) {
	lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"client.address": "198.51.100.9"}), resource: s3accessResource()})
	if got := attrStr(t, lr.Attributes(), "client.address"); got != "198.51.100.9" {
		t.Fatalf("client.address = %q", got)
	}
	absent(t, lr.Attributes(), "source.address")
}

func TestConsume_S3Access_UnlistedAccount_NoAccountName(t *testing.T) {
	res := map[string]any{"aws.s3.bucket": "bucket-name", "cloud.account.id": "444455556666"}
	_, got := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(nil), resource: res})
	if _, ok := got["cloud.account.name"]; ok {
		t.Fatalf("alias set for an unlisted account: %v", got)
	}
}

func TestConsume_S3Access_StatusClasses_Severity(t *testing.T) {
	cases := []struct {
		status int64
		num    plog.SeverityNumber
		text   string
	}{
		{200, plog.SeverityNumberInfo, "INFO"},
		{404, plog.SeverityNumberWarn, "WARN"},
		{503, plog.SeverityNumberError, "ERROR"},
	}
	for _, c := range cases {
		lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"http.response.status_code": c.status}), resource: s3accessResource()})
		s3accessSeverity(t, lr, c.num, c.text)
	}
}

func TestConsume_S3Access_NoStatus_SeverityUnsetBodyKept(t *testing.T) {
	lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"http.response.status_code": nil}), resource: s3accessResource()})
	s3accessSeverity(t, lr, plog.SeverityNumberUnspecified, "")
	if got := lr.Body().Str(); got != "BATCH.DELETE.OBJECT bucket-name/.chunks/ab/abcdef 174KiB" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_S3Access_NoKey_PathFromURL(t *testing.T) {
	attrs := s3accessAttrs(map[string]any{"aws.s3.key": nil, "url.path": "/bucket-name/.chunks/cd/data01", "rpc.method": "REST.GET.OBJECT", "http.response.body.size": nil})
	lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: attrs, resource: s3accessResource()})
	if got := lr.Body().Str(); got != "REST.GET.OBJECT bucket-name/.chunks/cd/data01 204" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_S3Access_BucketLevelCall_NoKey(t *testing.T) {
	attrs := s3accessAttrs(map[string]any{"aws.s3.key": nil, "rpc.method": "REST.GET.VERSIONING", "url.path": "/bucket-name", "http.response.body.size": int64(113)})
	lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: attrs, resource: s3accessResource()})
	if got := lr.Body().Str(); got != "REST.GET.VERSIONING bucket-name 204 113B" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_S3Access_PrincipalShortening_Service(t *testing.T) {
	cases := map[string]string{
		"arn:aws:sts::111122223333:assumed-role/ops-role/session-1": "session-1",
		"arn:aws:iam::111122223333:root":                            "root",
	}
	for arn, want := range cases {
		_, res := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"user.id": arn}), resource: s3accessResource()})
		if res["service.name"] != want {
			t.Fatalf("service.name for %s = %v, want %s", arn, res["service.name"], want)
		}
	}
}

func TestConsume_S3Access_NoPrincipalNoBucket_NoHostOrService(t *testing.T) {
	attrs := s3accessAttrs(map[string]any{"user.id": nil})
	lr, res := s3accessRun(t, s3accessConfig(), input{attrs: attrs})
	for _, k := range []string{"host.name", "service.name", "cloud.account.name"} {
		if _, ok := res[k]; ok {
			t.Fatalf("%s present: %v", k, res)
		}
	}
	if lr.Body().Str() == "" {
		t.Fatal("body empty")
	}
}

func TestConsume_S3Access_ExistingBodyNothingToBuild_Kept(t *testing.T) {
	lr, _ := s3accessRun(t, s3accessConfig(), input{body: "raw s3 line", attrs: map[string]any{"log.source": "s3access"}})
	if got := lr.Body().Str(); got != "raw s3 line" {
		t.Fatalf("body = %q", got)
	}
}

func TestConsume_S3Access_WrongTypedAttributes_NoPanic(t *testing.T) {
	attrs := s3accessAttrs(map[string]any{"http.response.status_code": "x", "http.response.body.size": "big", "user.id": int64(5), "aws.s3.key": true})
	lr, _ := s3accessRun(t, s3accessConfig(), input{attrs: attrs, resource: s3accessResource()})
	if lr.Body().Str() == "" {
		t.Fatal("body empty")
	}
}

func TestConsume_S3Access_OtherSource_Unmatched(t *testing.T) {
	lr, res := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"log.source": "aws.s3.other"}), resource: s3accessResource()})
	if lr.Body().Str() != "" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if _, ok := res["host.name"]; ok {
		t.Fatalf("resource changed: %v", res)
	}
	absent(t, lr.Attributes(), "client.address")
}

func TestConsume_S3Access_AnonymousRequester_NoServiceName(t *testing.T) {
	for _, user := range []string{"-", "", " "} {
		_, res := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(map[string]any{"user.id": user}), resource: s3accessResource()})
		if v, ok := res["service.name"]; ok {
			t.Fatalf("user.id %q gave service.name %v", user, v)
		}
		if res["host.name"] != "bucket-name" {
			t.Fatalf("resource = %v", res)
		}
	}
}

func TestConsume_S3Access_NoAccountID_NoAccountName(t *testing.T) {
	lr, res := s3accessRun(t, s3accessConfig(), input{attrs: s3accessAttrs(nil), resource: map[string]any{"aws.s3.bucket": "bucket-name"}})
	if _, ok := res["cloud.account.name"]; ok {
		t.Fatalf("resource = %v", res)
	}
	if lr.Body().Str() == "" || res["service.name"] != "backup-sync" {
		t.Fatalf("body %q resource %v", lr.Body().Str(), res)
	}
}

func TestConsume_S3Access_ExistingOriginal_Kept(t *testing.T) {
	attrs := s3accessAttrs(map[string]any{"log.record.original": "first original"})
	lr, _ := s3accessRun(t, s3accessConfig(), input{body: "raw s3 line", attrs: attrs, resource: s3accessResource()})
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != "first original" {
		t.Fatalf("log.record.original = %q", got)
	}
}

func TestConsume_S3Access_MixedBatch_EachRecordKeepsItsOwnIdentity(t *testing.T) {
	rec := func(user, bucket, key string) map[string]any {
		return s3accessAttrs(map[string]any{"user.id": "arn:aws:iam::111122223333:user/" + user, "aws.s3.bucket": bucket, "aws.s3.key": key})
	}
	outs := lbBatch(t, s3accessConfig(), map[string]any{"cloud.account.id": "111122223333"}, []map[string]any{
		rec("alice", "bucket-one", "k1"), rec("bob", "bucket-two", "k2"), rec("alice", "bucket-one", "k3"),
	})
	want := []struct{ host, svc, body string }{
		{"bucket-one", "alice", "BATCH.DELETE.OBJECT bucket-one/k1 204 174KiB"},
		{"bucket-one", "alice", "BATCH.DELETE.OBJECT bucket-one/k3 204 174KiB"},
		{"bucket-two", "bob", "BATCH.DELETE.OBJECT bucket-two/k2 204 174KiB"},
	}
	if len(outs) != 3 || outs[0].grp != outs[1].grp || outs[1].grp == outs[2].grp {
		t.Fatalf("grouping wrong: %v", outs)
	}
	for i, w := range want {
		if outs[i].res["host.name"] != w.host || outs[i].res["service.name"] != w.svc {
			t.Fatalf("record %d resource = %v, want host %s service %s", i, outs[i].res, w.host, w.svc)
		}
		if got := outs[i].lr.Body().Str(); got != w.body {
			t.Fatalf("record %d body = %q, want %q", i, got, w.body)
		}
	}
}
