// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logruslogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the Logrus JSON log processor.
type Config struct {
	// ServiceName is the service name to match against.
	// Matches if service.name, container.name, or k8s.container.name contains this value.
	// Defaults to empty (process all JSON logs).
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to rewrite the log body to the msg field.
	// Defaults to true.
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity controls whether to set OTEL severity from parsed level.
	// Defaults to true.
	AdjustSeverity bool `mapstructure:"adjust_severity"`

	// ParseTimestamp controls whether to parse the time field for log timestamp.
	// Defaults to false (keep original timestamp).
	ParseTimestamp bool `mapstructure:"parse_timestamp"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
