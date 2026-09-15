// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cronlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the cron log processor.
type Config struct {
	// RewriteMessages controls whether to rewrite the log body to a cleaner format.
	// Defaults to true.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
