// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the resourceversion processor.
type Config struct {
	// Role is the collector role (e.g., "edge", "gateway", "scrape")
	// The version attribute will be named "collector.{role}.version"
	Role string `mapstructure:"role"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.Role == "" {
		return errors.New("role must be specified")
	}
	return nil
}
