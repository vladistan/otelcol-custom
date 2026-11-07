// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"errors"
	"slices"

	"go.opentelemetry.io/collector/component"
)

// Config holds one block per fill rule. Every rule is on and ready to use without any setting.
type Config struct {
	HostName    HostNameConfig    `mapstructure:"host_name"`
	ServiceName ServiceNameConfig `mapstructure:"service_name"`
	AuditBody   AuditBodyConfig   `mapstructure:"audit_body"`
}

// HostNameConfig lists the attributes host.name is taken from, first non-empty wins.
type HostNameConfig struct {
	Disabled bool     `mapstructure:"disabled"`
	Sources  []string `mapstructure:"sources"`
}

// ServiceNameConfig names the service.name given to each kind of record.
type ServiceNameConfig struct {
	Disabled     bool   `mapstructure:"disabled"`
	K8sEvent     string `mapstructure:"k8s_event"`
	K8sPodStatus string `mapstructure:"k8s_pod_status"`
	GCPAudit     string `mapstructure:"gcp_audit"`
}

type AuditBodyConfig struct {
	Disabled bool `mapstructure:"disabled"`
}

func (cfg *Config) Validate() error {
	var errs []error
	if !cfg.HostName.Disabled && (len(cfg.HostName.Sources) == 0 || slices.Contains(cfg.HostName.Sources, "")) {
		errs = append(errs, errors.New("host_name.sources must list at least one attribute name and no blank ones"))
	}
	if !cfg.ServiceName.Disabled {
		for key, name := range map[string]string{
			"service_name.k8s_event":      cfg.ServiceName.K8sEvent,
			"service_name.k8s_pod_status": cfg.ServiceName.K8sPodStatus,
			"service_name.gcp_audit":      cfg.ServiceName.GCPAudit,
		} {
			if name == "" {
				errs = append(errs, errors.New(key+" must not be empty"))
			}
		}
	}
	return errors.Join(errs...)
}

var _ component.Config = (*Config)(nil)
