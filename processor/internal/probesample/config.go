// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package probesample

import (
	"fmt"
	"math"
)

// Config holds the classification lists and the keep ratio.
type Config struct {
	// Paths are exact request paths that mark a health probe.
	Paths []string `mapstructure:"paths"`

	// Agents are user-agent prefixes that mark a health probe.
	Agents []string `mapstructure:"agents"`

	// KeepRatio is the share of probes kept, from 0 to 1.
	KeepRatio float64 `mapstructure:"keep_ratio"`
}

var (
	defaultPaths  = []string{"/healthz", "/health", "/api/health", "/probe", "/api/healthz", "/ready", "/readyz", "/livez"}
	defaultAgents = []string{"kube-probe/", "ELB-HealthChecker/", "HealthProbe", "Amazon-Route53-Health-Check-Service"}
)

// Default returns the built-in probe lists and keep ratio. The agents are the Kubernetes probe,
// the load balancer health checker, a generic HealthProbe agent and the Route53 health check.
// A list set in the configuration extends these lists; the sampler always adds the built-in entries.
func Default() Config {
	return Config{
		Paths:     append([]string(nil), defaultPaths...),
		Agents:    append([]string(nil), defaultAgents...),
		KeepRatio: 0.2,
	}
}

func (cfg *Config) Validate() error {
	if math.IsNaN(cfg.KeepRatio) || cfg.KeepRatio < 0 || cfg.KeepRatio > 1 {
		return fmt.Errorf("keep_ratio must be between 0 and 1, got %v", cfg.KeepRatio)
	}
	for i, p := range cfg.Paths {
		if p == "" {
			return fmt.Errorf("paths[%d] is empty", i)
		}
	}
	for i, a := range cfg.Agents {
		if a == "" {
			return fmt.Errorf("agents[%d] is empty", i)
		}
	}
	return nil
}
