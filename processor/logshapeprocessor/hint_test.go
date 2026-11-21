// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"
)

func hintConfig(branch string, hint Hint) *Config {
	cfg := defaultConfig()
	cfg.Hints = map[string]Hint{branch: hint}
	return cfg
}

func TestHint_ServiceMustMatchForTheBranchToClaim(t *testing.T) {
	h := newHarness(t, hintConfig("nginx", Hint{Service: "tea-frontend"}))

	hit, _ := h.run(input{body: nginxLine, resource: map[string]any{"service.name": "tea-frontend"}})
	miss, _ := h.run(input{body: nginxLine, resource: map[string]any{"service.name": "other"}})
	none, _ := h.run(input{body: nginxLine})

	if attrStr(t, hit.Attributes(), "http.request.method") != "POST" {
		t.Errorf("the hinted service was not claimed: %v", hit.Attributes().AsRaw())
	}
	for name, lr := range map[string]interface{ Len() int }{"other service": miss.Attributes(), "no service": none.Attributes()} {
		if lr.Len() != 0 {
			t.Errorf("%s was claimed", name)
		}
	}
	if got := h.counter(unmatchedMetric, "source"); got["none"] != 2 {
		t.Errorf("unmatched = %v, want 2", got)
	}
}

func TestHint_ScopeMustMatch(t *testing.T) {
	h := newHarness(t, hintConfig("nginx", Hint{Scope: "access"}))

	hit, _ := h.run(input{body: nginxLine, scope: "access"})
	miss, _ := h.run(input{body: nginxLine, scope: "error"})

	if hit.Attributes().Len() == 0 || miss.Attributes().Len() != 0 {
		t.Errorf("hit %v miss %v", hit.Attributes().AsRaw(), miss.Attributes().AsRaw())
	}
}

func TestHint_SourceMustMatch(t *testing.T) {
	h := newHarness(t, hintConfig("nginx", Hint{Source: "docker"}))

	hit, _ := h.run(input{body: nginxLine, attrs: map[string]any{"log.source": "docker"}})
	miss, _ := h.run(input{body: nginxLine, attrs: map[string]any{"log.source": "file"}})

	if attrStr(t, hit.Attributes(), "http.request.method") != "POST" || attrStr(t, miss.Attributes(), "log.source") != "file" {
		t.Errorf("hit %v miss %v", hit.Attributes().AsRaw(), miss.Attributes().AsRaw())
	}
	if _, ok := miss.Attributes().Get("http.request.method"); ok {
		t.Error("a record from another source was claimed")
	}
}

func TestHint_AllSetFieldsMustMatch(t *testing.T) {
	h := newHarness(t, hintConfig("nginx", Hint{Service: "tea-frontend", Scope: "access"}))

	both, _ := h.run(input{body: nginxLine, scope: "access", resource: map[string]any{"service.name": "tea-frontend"}})
	one, _ := h.run(input{body: nginxLine, scope: "error", resource: map[string]any{"service.name": "tea-frontend"}})

	if _, ok := both.Attributes().Get("http.request.method"); !ok {
		t.Error("a record matching both fields was not claimed")
	}
	if _, ok := one.Attributes().Get("http.request.method"); ok {
		t.Error("a record matching one field was claimed")
	}
}

func TestHint_DefaultIsNone_EveryRecordIsClaimedByShape(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: nginxLine, resource: map[string]any{"service.name": "anything"}})

	if attrStr(t, lr.Attributes(), "http.request.method") != "POST" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestHint_OnlyTheNamedBranchIsRestricted(t *testing.T) {
	h := newHarness(t, hintConfig("nginx", Hint{Service: "tea-frontend"}))

	lr, _ := h.run(input{body: klogLine, ts: recordTime, resource: map[string]any{"service.name": "other"}})

	if attrStr(t, lr.Attributes(), "log.record.original") != klogLine {
		t.Errorf("the klog branch was restricted: %v", lr.Attributes().AsRaw())
	}
}

func TestValidate_HintForAnUnknownBranch_Fails(t *testing.T) {
	err := hintConfig("nope", Hint{Service: "x"}).Validate()

	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Validate = %v, want an error naming nope", err)
	}
}
