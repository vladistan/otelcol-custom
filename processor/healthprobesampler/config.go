// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package healthprobesampler

import (
	"errors"

	"go.opentelemetry.io/collector/component"

	"github.com/vladistan/otelcol-custom/processor/internal/probesample"
)

// Config sets the probe lists, the keep ratio and the attribute names the processor reads.
type Config struct {
	probesample.Config `mapstructure:",squash"`

	// Enabled turns sampling on; when false every record passes unchanged.
	Enabled bool `mapstructure:"enabled"`

	StatusAttribute string `mapstructure:"status_attribute"`
	PathAttribute   string `mapstructure:"path_attribute"`
	AgentAttribute  string `mapstructure:"agent_attribute"`
}

func (cfg *Config) Validate() error {
	if err := cfg.Config.Validate(); err != nil {
		return err
	}
	if cfg.StatusAttribute == "" || cfg.PathAttribute == "" || cfg.AgentAttribute == "" {
		return errors.New("status_attribute, path_attribute and agent_attribute must be set")
	}
	return nil
}

var _ component.Config = (*Config)(nil)
