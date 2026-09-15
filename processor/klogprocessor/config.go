// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package klogprocessor

// Config holds configuration for the klog processor.
type Config struct {
	// ServiceName filters logs to only process those from this service.
	// Matches against service.name, container.name, or k8s.container.name.
	// If empty, processes all logs that match the klog format.
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages replaces the body with the extracted message.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity sets OTEL severity based on klog level.
	// Default: true
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}
