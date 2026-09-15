// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kernellogprocessor

// Config holds configuration for the kernel log processor.
type Config struct {
	// RewriteMessages replaces the body with the extracted message.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`

	// AdjustSeverity sets OTEL severity based on detected error type.
	// Default: true
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}
