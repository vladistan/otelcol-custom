// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pgbouncerlogprocessor

import "go.opentelemetry.io/collector/component"

// Config defines configuration for the PgBouncer log processor.
type Config struct {
	// SourceAttribute is the attribute to read the log message from.
	// If empty, reads from the log record body.
	SourceAttribute string `mapstructure:"source_attribute"`

	// ServiceName sets resource.attributes.service.name if not already set.
	// Default: "pgbouncer"
	ServiceName string `mapstructure:"service_name"`

	// SetServiceName controls whether to set the service.name attribute.
	// Default: true
	SetServiceName bool `mapstructure:"set_service_name"`

	// ParseTimestamp controls whether to parse and set the log timestamp.
	// Default: true
	ParseTimestamp bool `mapstructure:"parse_timestamp"`

	// PreserveOriginal keeps the original message in attributes.original_message.
	// Default: true
	PreserveOriginal bool `mapstructure:"preserve_original"`
}

var _ component.Config = (*Config)(nil)

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}
