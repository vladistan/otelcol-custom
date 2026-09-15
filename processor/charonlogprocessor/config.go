// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package charonlogprocessor

// Config holds configuration for the charon log processor.
type Config struct {
	// ServiceName filters logs to only process those from this service.
	// Default: "charon"
	ServiceName string `mapstructure:"service_name"`

	// RewriteMessages replaces the body with the extracted message.
	// Default: true
	RewriteMessages bool `mapstructure:"rewrite_messages"`
}
