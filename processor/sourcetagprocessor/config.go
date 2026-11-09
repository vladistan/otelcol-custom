// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
)

const (
	providerAWS   = "aws"
	providerGCP   = "gcp"
	providerAzure = "azure"
)

// Config selects the rule set. Provider has no default, so a missing setting fails at startup.
type Config struct {
	Provider string `mapstructure:"provider"`
}

func (cfg *Config) Validate() error {
	switch cfg.Provider {
	case providerAWS, providerGCP, providerAzure:
		return nil
	}
	return fmt.Errorf("provider must be one of %s, %s, %s; got %q", providerAWS, providerGCP, providerAzure, cfg.Provider)
}

var _ component.Config = (*Config)(nil)
