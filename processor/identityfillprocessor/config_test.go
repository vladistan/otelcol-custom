// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"slices"
	"strings"
	"testing"
)

func defaultConfig() *Config {
	return NewFactory().CreateDefaultConfig().(*Config)
}

func TestDefaultConfig_ValidatesWithoutAnyRuleSet(t *testing.T) {
	if err := defaultConfig().Validate(); err != nil {
		t.Fatalf("default config does not validate: %v", err)
	}
}

func TestDefaultConfig_Values(t *testing.T) {
	cfg := defaultConfig()

	wantSources := []string{"k8s.node.name", "k8s.cluster.name", "gcp.label.cluster_name"}
	if !slices.Equal(cfg.HostName.Sources, wantSources) {
		t.Errorf("host sources = %v, want %v", cfg.HostName.Sources, wantSources)
	}
	names := cfg.ServiceName
	if names.K8sEvent != "k8s-events" || names.K8sPodStatus != "k8s-pod-status" || names.GCPAudit != "gcp-audit" {
		t.Errorf("service names = %+v", names)
	}
	if cfg.HostName.Disabled || cfg.ServiceName.Disabled || cfg.AuditBody.Disabled {
		t.Errorf("a rule is off by default: %+v", cfg)
	}
}

func TestValidate_RejectsAnEnabledRuleWithNothingToUse(t *testing.T) {
	cases := map[string]struct {
		edit func(*Config)
		want string
	}{
		"no host sources":    {func(c *Config) { c.HostName.Sources = nil }, "host_name.sources"},
		"blank host source":  {func(c *Config) { c.HostName.Sources = []string{"k8s.node.name", ""} }, "host_name.sources"},
		"no event name":      {func(c *Config) { c.ServiceName.K8sEvent = "" }, "service_name.k8s_event"},
		"no pod status name": {func(c *Config) { c.ServiceName.K8sPodStatus = "" }, "service_name.k8s_pod_status"},
		"no gcp audit name":  {func(c *Config) { c.ServiceName.GCPAudit = "" }, "service_name.gcp_audit"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := defaultConfig()
			tc.edit(cfg)

			err := cfg.Validate()

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestValidate_AcceptsEmptySettingsOfADisabledRule(t *testing.T) {
	cfg := defaultConfig()
	cfg.HostName = HostNameConfig{Disabled: true}
	cfg.ServiceName = ServiceNameConfig{Disabled: true}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for disabled rules", err)
	}
}
