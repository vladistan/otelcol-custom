// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kafkalogprocessor

import "go.opentelemetry.io/collector/component"

// Config defines configuration for the kafkalog processor.
type Config struct {
	// ServiceName is the service.name or container.name to match for processing.
	// Default: "kafka"
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to replace the log body with the extracted message.
	// When true, the body is replaced with a cleaned format: [logger] message
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// ExtractLogger controls whether to extract the Kafka logger/class name.
	// When true, sets kafka.logger attribute.
	// Default: true
	ExtractLogger bool `mapstructure:"extract_logger"`

	// AdjustSeverity controls whether to set OTEL severity from Kafka log level.
	// Default: true
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// Validate validates the processor configuration.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
