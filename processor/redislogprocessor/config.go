// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package redislogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the Redis log processor.
type Config struct {
	// ServiceName is the service name to match for processing.
	// Only logs with this service name will be processed.
	// Default: "redis"
	ServiceName string `mapstructure:"service_name"`

	// SourceAttribute specifies an attribute to read the log line from.
	// If empty or not found, falls back to body.
	SourceAttribute string `mapstructure:"source_attribute"`

	// SetServiceName controls whether to set resource.service.name if not already set.
	// Default: true
	SetServiceName bool `mapstructure:"set_service_name"`

	// PreserveOriginal saves the original message to original_message attribute.
	// Default: true
	PreserveOriginal bool `mapstructure:"preserve_original"`

	// RewriteMessages controls whether to rewrite the body to a cleaner format.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity controls whether to set OTEL severity from Redis log level.
	// Default: true
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	return nil
}
