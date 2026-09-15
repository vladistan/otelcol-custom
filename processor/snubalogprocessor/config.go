// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package snubalogprocessor

import "go.opentelemetry.io/collector/component"

// Config defines configuration for the snubalog processor.
type Config struct {
	// ServiceName is the service.name to match for processing.
	// Only logs with this service.name resource attribute will be processed.
	// Default: "snuba"
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to replace the log body with the extracted message.
	// When true, the body is replaced with the parsed message field.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// ExtractTarget controls whether to extract the Rust target module path.
	// When true, sets snuba.target attribute.
	// Default: true
	ExtractTarget bool `mapstructure:"extract_target"`

	// ExtractStorage controls whether to extract the storage field.
	// When true, sets snuba.storage attribute.
	// Default: true
	ExtractStorage bool `mapstructure:"extract_storage"`
}

// Validate validates the processor configuration.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
