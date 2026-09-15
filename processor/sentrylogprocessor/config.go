// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sentrylogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines the configuration for the Sentry log processor.
type Config struct {
	// SourceAttribute is the attribute containing the log line to parse.
	// If empty, the processor will use the log body.
	SourceAttribute string `mapstructure:"source_attribute"`

	// SetServiceName controls whether to set the service.name resource attribute.
	SetServiceName bool `mapstructure:"set_service_name"`

	// ServiceName is the service name to set when SetServiceName is true.
	// Defaults to "sentry".
	ServiceName string `mapstructure:"service_name"`

	// ParseTimestamp controls whether to parse and use the log's timestamp.
	ParseTimestamp bool `mapstructure:"parse_timestamp"`

	// PreserveOriginal controls whether to preserve the original message.
	PreserveOriginal bool `mapstructure:"preserve_original"`

	// ExtractHTTPFields controls whether to extract HTTP request/response fields.
	ExtractHTTPFields bool `mapstructure:"extract_http_fields"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	return nil
}
