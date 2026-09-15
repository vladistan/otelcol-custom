// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the Cisco log processor.
type Config struct {
	// SourceAttribute specifies the attribute to search for Cisco mnemonic patterns.
	// Defaults to "original_message" (the raw syslog message before parsing).
	SourceAttribute string `mapstructure:"source_attribute"`

	// SetHostname controls whether to set host.name from net.peer.name
	// when host.name is not already set.
	// Defaults to true.
	SetHostname bool `mapstructure:"set_hostname"`

	// CleanBody controls whether to remove <priority> prefix from body.
	// Defaults to true.
	CleanBody bool `mapstructure:"clean_body"`

	// PortDatabasePath is the path to the YAML port name database file.
	// Optional - if not set, port name enhancement is disabled.
	PortDatabasePath string `mapstructure:"port_database_path"`

	// RewriteMessages controls whether to clean up and enhance log messages.
	// When enabled, removes redundant info (facility/severity already in attributes)
	// and enhances port names with friendly aliases from port database.
	// Defaults to true.
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity controls whether to override OTEL severity for certain events.
	// For example, interface up/down events are set to WARN instead of ERROR.
	// Defaults to true.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
