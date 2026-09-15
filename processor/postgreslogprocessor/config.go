// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package postgreslogprocessor

import "go.opentelemetry.io/collector/component"

// Config defines configuration for the postgreslog processor.
type Config struct {
	// ServiceName is the service.name or container.name to match for processing.
	// Default: "postgres"
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to replace the log body with the extracted message.
	// When true, the body is replaced with cleaned format: [level] message
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// ExtractDetails controls whether to extract DETAIL/STATEMENT/HINT lines.
	// When true, sets postgres.detail, postgres.statement, postgres.hint attributes.
	// Default: true
	ExtractDetails bool `mapstructure:"extract_details"`

	// AdjustSeverity controls whether to set OTEL severity from PostgreSQL log level.
	// Default: true
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// Validate validates the processor configuration.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
