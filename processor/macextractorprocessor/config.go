// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package macextractorprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the MAC extractor processor.
type Config struct {
	// TargetAttribute specifies where to store extracted MAC addresses.
	// Defaults to "client.mac" (ECS semantic convention for client system).
	TargetAttribute string `mapstructure:"target_attribute"`

	// SearchBody controls whether to search the log body for MAC addresses.
	// Handles both string bodies and complex map structures (e.g., journald logs).
	// Defaults to true.
	SearchBody bool `mapstructure:"search_body"`

	// AttributesToSearch specifies which attributes to search for MAC addresses.
	// Only these specific attributes will be searched (whitelist approach).
	// Defaults to ["original_message"] - the raw syslog message before parsing.
	// Set to empty to disable attribute searching entirely.
	AttributesToSearch []string `mapstructure:"attributes_to_search"`

	// ExtractAll controls whether to extract all MACs or just the first one.
	// If true, stores multiple MACs as a comma-separated list.
	// Defaults to false (first MAC only).
	ExtractAll bool `mapstructure:"extract_all"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
