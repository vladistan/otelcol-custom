// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package nginxlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the Nginx log processor.
type Config struct {
	// SourceAttribute specifies the attribute containing the raw log line.
	// If empty, uses the log body.
	// Defaults to empty (use body).
	SourceAttribute string `mapstructure:"source_attribute"`

	// SetServiceName controls whether to set service.name to "nginx"
	// when not already set.
	// Defaults to true.
	SetServiceName bool `mapstructure:"set_service_name"`

	// ServiceName is the service name to set if SetServiceName is true.
	// Defaults to "nginx".
	ServiceName string `mapstructure:"service_name"`

	// ParseTimestamp controls whether to parse the timestamp from the log.
	// If true, the log record timestamp is updated from the parsed value.
	// Defaults to false (keep original timestamp).
	ParseTimestamp bool `mapstructure:"parse_timestamp"`

	// PreserveOriginal controls whether to keep the original log body.
	// If true, the original body is preserved in attributes["original_message"].
	// If false, the body is replaced with a cleaned version.
	// Defaults to false.
	PreserveOriginal bool `mapstructure:"preserve_original"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
