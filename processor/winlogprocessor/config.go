// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
)

// DropRule drops an event when every field it sets matches. Values inside one
// field are alternatives; an unset field matches anything.
type DropRule struct {
	Name      string   `mapstructure:"name"`
	Channels  []string `mapstructure:"channels"`
	EventIDs  []string `mapstructure:"event_ids"` // "4624" or an inclusive range "10000-10010"
	Levels    []int    `mapstructure:"levels"`    // 0-5, from the event XML
	Providers []string `mapstructure:"providers"`
}

// Config holds the drop rules and the original_message cap. A rule list in the
// config replaces the defaults.
type Config struct {
	DropRules []DropRule `mapstructure:"drop_rules"`
	// OriginalMessageMaxBytes cuts original_message (the event XML); 0 keeps it whole.
	OriginalMessageMaxBytes int `mapstructure:"original_message_max_bytes"`
}

func (cfg *Config) Validate() error {
	if cfg.OriginalMessageMaxBytes < 0 {
		return fmt.Errorf("original_message_max_bytes must not be negative, got %d", cfg.OriginalMessageMaxBytes)
	}
	_, err := compileRules(cfg.DropRules)
	return err
}

var _ component.Config = (*Config)(nil)
