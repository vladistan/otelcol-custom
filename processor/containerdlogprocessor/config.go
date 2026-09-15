// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containerdlogprocessor

import (
	"go.opentelemetry.io/collector/component"
)

// Config holds configuration for the containerd log processor.
type Config struct {
	// ServiceName is the expected service name to match (default: containerd.service)
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages controls whether to rewrite body to cleaner message
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity controls whether to set OTEL severity from log level
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

func createDefaultConfig() component.Config {
	return &Config{
		ServiceName:     "containerd.service",
		RewriteMessages: true,
		AdjustSeverity:  true,
	}
}
