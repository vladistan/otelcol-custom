// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snoozelogprocessor

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
)

// MatchRule selects records by exact resource attributes k8s.namespace.name and k8s.container.name.
type MatchRule struct {
	Namespace string `mapstructure:"namespace"`
	Container string `mapstructure:"container"`
}

// Config lists the containers whose lines are parsed.
type Config struct {
	Match []MatchRule `mapstructure:"match"`
}

func (cfg *Config) Validate() error {
	if len(cfg.Match) == 0 {
		return errors.New("match must list at least one rule")
	}
	for i, rule := range cfg.Match {
		if rule.Namespace == "" || rule.Container == "" {
			return fmt.Errorf("match[%d]: namespace and container are both required", i)
		}
	}
	return nil
}

var _ component.Config = (*Config)(nil)
