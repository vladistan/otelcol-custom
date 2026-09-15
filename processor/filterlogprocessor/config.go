// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filterlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the filterlog processor.
type Config struct {
	// SourceAttribute is the attribute containing the raw filterlog message.
	// Default: "original_message"
	SourceAttribute string `mapstructure:"source_attribute"`

	// RewriteMessages controls whether to rewrite the body to a clean format.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	return nil
}
