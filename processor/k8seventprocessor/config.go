// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8seventprocessor

import "go.opentelemetry.io/collector/component"

// Config defines the configuration for the Kubernetes Event processor.
type Config struct {
	// RewriteMessages controls whether to set body to human-readable message
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// AdjustSeverity controls whether to set severity based on event type
	AdjustSeverity bool `mapstructure:"adjust_severity"`
	// OTTLParity sets every field of the kube-v0 transform/k8s_objects block on event and pod
	// records. Body and severity follow that block and override the two switches above.
	OTTLParity bool `mapstructure:"ottl_parity"`
}

// Validate validates the processor configuration.
func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
