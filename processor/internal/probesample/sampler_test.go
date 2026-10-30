// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package probesample

import (
	"math/rand/v2"
	"testing"
)

func newSampler(cfg Config) *Sampler {
	return New(cfg, rand.New(rand.NewPCG(1, 2)).Float64)
}

func TestClassify_EachDefaultPath_IsProbe(t *testing.T) {
	s := newSampler(Default())
	for _, path := range Default().Paths {
		for _, status := range []int{200, 302} {
			if !s.Classify(status, path, "") {
				t.Errorf("Classify(%d, %q, \"\") = false, want true", status, path)
			}
		}
	}
}

func TestClassify_EachDefaultAgentOnNonProbePath_IsProbe(t *testing.T) {
	s := newSampler(Default())
	for _, agent := range []string{"kube-probe/1.29", "ELB-HealthChecker/2.0"} {
		if !s.Classify(200, "/orders", agent) {
			t.Errorf("agent %q on /orders = false, want true (a match on either rule classes the record as a probe)", agent)
		}
	}
}

func TestClassify_NonProbePathAndAgent_IsNotProbe(t *testing.T) {
	s := newSampler(Default())

	if s.Classify(200, "/orders", "curl/8.0") {
		t.Error("ordinary request classed as probe")
	}
}

func TestClassify_ProbeStatusOutside200And302_IsNotProbe(t *testing.T) {
	s := newSampler(Default())
	for _, status := range []int{201, 204, 301, 304, 404, 500, 503, 0} {
		if s.Classify(status, "/healthz", "kube-probe/1.29") {
			t.Errorf("status %d classed as probe", status)
		}
	}
}

func TestClassify_QueryString_MatchesPathWithoutQuery(t *testing.T) {
	s := newSampler(Default())

	if !s.Classify(200, "/healthz?x=1", "") {
		t.Error("/healthz?x=1 not classed as probe")
	}
}

func TestClassify_TrailingSlash_MatchesListedPath(t *testing.T) {
	s := newSampler(Default())

	if !s.Classify(200, "/api/healthz/", "") {
		t.Error("/api/healthz/ not classed as probe")
	}
}

func TestClassify_PathIsExact_NotPrefix(t *testing.T) {
	s := newSampler(Default())
	for _, path := range []string{"/healthz-admin", "/healthzz", "/api/health/deep", "/x/healthz"} {
		if s.Classify(200, path, "") {
			t.Errorf("path %q classed as probe", path)
		}
	}
}

func TestClassify_AgentIsPrefixAndCaseSensitive(t *testing.T) {
	s := newSampler(Default())

	if s.Classify(200, "/orders", "my-kube-probe/1.0") {
		t.Error("agent matched on a non-prefix")
	}
	if s.Classify(200, "/orders", "KUBE-PROBE/1.0") {
		t.Error("agent matched case-insensitively")
	}
}

func TestClassify_NoPathNoAgent_IsNotProbe(t *testing.T) {
	s := newSampler(Default())

	if s.Classify(200, "", "") {
		t.Error("record without path and agent classed as probe")
	}
}

func TestClassify_CustomLists_ReplaceDefaults(t *testing.T) {
	s := newSampler(Config{Paths: []string{"/ping"}, Agents: []string{"uptime-bot/"}, KeepRatio: 1})

	if !s.Classify(200, "/ping", "") || !s.Classify(302, "/x", "uptime-bot/3") {
		t.Error("custom path or agent not matched")
	}
	if s.Classify(200, "/healthz", "kube-probe/1.29") {
		t.Error("default list still active after override")
	}
}

func TestKeep_RatioOne_KeepsAll(t *testing.T) {
	s := newSampler(Config{KeepRatio: 1})
	for i := 0; i < 1000; i++ {
		if !s.Keep() {
			t.Fatal("Keep() = false at ratio 1")
		}
	}
}

func TestKeep_RatioZero_KeepsNone(t *testing.T) {
	s := newSampler(Config{KeepRatio: 0})
	for i := 0; i < 1000; i++ {
		if s.Keep() {
			t.Fatal("Keep() = true at ratio 0")
		}
	}
}

func TestKeep_RatioPointTwo_KeepsBetween18And22PercentOf10000(t *testing.T) {
	s := newSampler(Config{KeepRatio: 0.2})
	kept := 0
	for i := 0; i < 10000; i++ {
		if s.Keep() {
			kept++
		}
	}

	if kept < 1800 || kept > 2200 {
		t.Errorf("kept %d of 10000, want 1800-2200", kept)
	}
}

func TestKeep_UsesInjectedSource(t *testing.T) {
	draws := []float64{0.1, 0.5, 0.19, 0.2}
	i := 0
	s := New(Config{KeepRatio: 0.2}, func() float64 { v := draws[i]; i++; return v })

	got := []bool{s.Keep(), s.Keep(), s.Keep(), s.Keep()}

	want := []bool{true, false, true, false}
	for j := range want {
		if got[j] != want[j] {
			t.Errorf("draw %v: Keep() = %v, want %v", draws[j], got[j], want[j])
		}
	}
}
