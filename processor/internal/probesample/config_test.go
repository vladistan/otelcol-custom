// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package probesample

import (
	"math"
	"testing"
)

func TestConfigValidates(t *testing.T) {
	if err := (&Config{}).Validate(); err != nil {
		t.Fatalf("empty config does not validate: %v", err)
	}
}

func TestDefault_MatchesDocumentedDefaults(t *testing.T) {
	cfg := Default()

	wantPaths := []string{"/healthz", "/health", "/api/health", "/probe", "/api/healthz"}
	wantAgents := []string{"kube-probe/", "ELB-HealthChecker/"}
	if len(cfg.Paths) != len(wantPaths) || len(cfg.Agents) != len(wantAgents) {
		t.Fatalf("default lists = %v / %v", cfg.Paths, cfg.Agents)
	}
	for i, p := range wantPaths {
		if cfg.Paths[i] != p {
			t.Errorf("paths[%d] = %q, want %q", i, cfg.Paths[i], p)
		}
	}
	for i, a := range wantAgents {
		if cfg.Agents[i] != a {
			t.Errorf("agents[%d] = %q, want %q", i, cfg.Agents[i], a)
		}
	}
	if cfg.KeepRatio != 0.2 {
		t.Errorf("keep ratio = %v, want 0.2", cfg.KeepRatio)
	}
}

func TestValidate_KeepRatioOutsideUnitInterval_Fails(t *testing.T) {
	for _, ratio := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		if err := (&Config{KeepRatio: ratio}).Validate(); err == nil {
			t.Errorf("keep_ratio %v validated, want an error", ratio)
		}
	}
}

func TestValidate_KeepRatioBounds_Pass(t *testing.T) {
	for _, ratio := range []float64{0, 0.2, 1} {
		if err := (&Config{KeepRatio: ratio}).Validate(); err != nil {
			t.Errorf("keep_ratio %v: %v", ratio, err)
		}
	}
}

func TestValidate_EmptyListEntry_Fails(t *testing.T) {
	if err := (&Config{Paths: []string{"/healthz", ""}}).Validate(); err == nil {
		t.Error("empty path entry validated, want an error")
	}
	if err := (&Config{Agents: []string{""}}).Validate(); err == nil {
		t.Error("empty agent entry validated, want an error")
	}
}
