// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	// Role is the collector role (e.g., "edge", "gateway", "scrape").
	// The version attribute is named "collector.{role}.version".
	Role string `mapstructure:"role"`

	// ShortHostName cuts host.name of each resource at its first dot, unless the
	// value is an IP address or has no dot. Off by default.
	ShortHostName bool `mapstructure:"short_host_name"`
}

var _ component.Config = (*Config)(nil)

// Validate requires a role because it names the version attribute.
func (cfg *Config) Validate() error {
	if cfg.Role == "" {
		return errors.New("role must be specified")
	}
	return nil
}
