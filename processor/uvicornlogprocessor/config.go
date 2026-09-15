// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the Uvicorn log processor.
type Config struct {
	// ServiceName is the service name to match against.
	// Matches if service.name or container.name contains this value.
	// Defaults to "uvicorn".
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to rewrite the log body to a cleaned format.
	// Defaults to true.
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// ExtractHTTPFields controls whether to extract HTTP request/response fields.
	// Defaults to true.
	ExtractHTTPFields bool `mapstructure:"extract_http_fields"`

	// AdjustSeverity controls whether to set OTEL severity from parsed level.
	// Defaults to true.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
