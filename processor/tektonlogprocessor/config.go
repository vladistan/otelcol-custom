// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tektonlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config holds the configuration for the tektonlog processor.
type Config struct {
	// ServiceName is the service name to match for processing (default: "controller")
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to rewrite log bodies to cleaner format
	RewriteMessages bool `mapstructure:"rewrite_messages"`
}

// Validate checks if the configuration is valid.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
