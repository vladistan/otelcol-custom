// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package syslogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the syslog processor.
type Config struct {
	// SourceAttribute specifies the attribute containing the raw syslog message.
	// Defaults to "message" (the attribute set by udplog/tcplog receivers after priority parsing).
	SourceAttribute string `mapstructure:"source_attribute"`

	// SetHostname controls whether to set host.name resource attribute from syslog hostname.
	// Defaults to true.
	SetHostname bool `mapstructure:"set_hostname"`

	// SetServiceName controls whether to set service.name resource attribute from syslog appname.
	// Defaults to true.
	SetServiceName bool `mapstructure:"set_service_name"`

	// MapSeverity controls whether to map syslog priority to OTEL severity.
	// Defaults to true.
	MapSeverity bool `mapstructure:"map_severity"`

	// PreserveOriginal controls whether to save the original message before parsing.
	// Defaults to true.
	PreserveOriginal bool `mapstructure:"preserve_original"`

	// CleanBody controls whether to set the body to just the message content.
	// Defaults to true.
	CleanBody bool `mapstructure:"clean_body"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
