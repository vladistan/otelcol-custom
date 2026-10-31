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
	Name      string           `mapstructure:"name"`
	Channels  []string         `mapstructure:"channels"`
	EventIDs  []string         `mapstructure:"event_ids"` // "4624" or an inclusive range "10000-10010"
	Levels    []int            `mapstructure:"levels"`    // 0-5, from the event XML
	Providers []string         `mapstructure:"providers"`
	Fields    []FieldCondition `mapstructure:"fields"`   // all must match
	TallyBy   []string         `mapstructure:"tally_by"` // event data names counted in the drop summary
}

// FieldCondition tests one event data field. Set equals (any listed value) or
// ends_with, not both. A field the event lacks never matches.
type FieldCondition struct {
	Name     string   `mapstructure:"name"`
	Equals   []string `mapstructure:"equals"`
	EndsWith string   `mapstructure:"ends_with"`
}

// DropSummaryConfig turns on the hourly summary of dropped events.
type DropSummaryConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// Config holds the drop rules and the original_message cap. A rule list in the
// config replaces the defaults.
type Config struct {
	DropRules   []DropRule        `mapstructure:"drop_rules"`
	DropSummary DropSummaryConfig `mapstructure:"drop_summary"`
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
