// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipextractorprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the IP extractor processor.
type Config struct {
	// TargetAttribute specifies where to store extracted IP addresses.
	// Defaults to "client.ip" (follows client.* convention alongside client.mac).
	TargetAttribute string `mapstructure:"target_attribute"`

	// SearchBody controls whether to search the log body for IP addresses.
	// Handles both string bodies and complex map structures (e.g., journald logs).
	// Defaults to true.
	SearchBody bool `mapstructure:"search_body"`

	// AttributesToSearch specifies which attributes to search for IP addresses.
	// Only these specific attributes will be searched (whitelist approach).
	// Defaults to ["original_message"] - the raw syslog message before parsing.
	// Set to empty to disable attribute searching entirely.
	AttributesToSearch []string `mapstructure:"attributes_to_search"`

	// ExtractAll controls whether to extract all IPs or just the first one.
	// If true, stores multiple IPs as a comma-separated list.
	// Defaults to false (first IP only).
	ExtractAll bool `mapstructure:"extract_all"`

	// ExtractIPv6 controls whether to also extract IPv6 addresses.
	// Defaults to false (IPv4 only).
	ExtractIPv6 bool `mapstructure:"extract_ipv6"`
}

// Validate checks the configuration for errors.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
