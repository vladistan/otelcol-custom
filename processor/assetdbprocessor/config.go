// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package assetdbprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the assetdb processor.
type Config struct {
	// DatabasePath is the path to the YAML asset database file.
	DatabasePath string `mapstructure:"database_path"`

	// SourceAttribute is the attribute name containing the MAC address to look up.
	// Defaults to "client.mac" if not specified (set by macextractorprocessor).
	SourceAttribute string `mapstructure:"source_attribute"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.DatabasePath == "" {
		return errors.New("database_path must be specified")
	}
	return nil
}
