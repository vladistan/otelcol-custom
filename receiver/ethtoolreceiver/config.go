// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config selects the interfaces to read and how often.
type Config struct {
	// CollectionInterval is the time between two reads.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// Drivers are the network drivers whose interfaces are read.
	Drivers []string `mapstructure:"drivers"`
	// Interfaces limits the read to these names; empty means every interface
	// with a matching driver.
	Interfaces []string `mapstructure:"interfaces"`
}

var _ component.Config = (*Config)(nil)

// Validate rejects an interval under one second and empty driver or interface names.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if len(cfg.Drivers) == 0 {
		return errors.New("drivers must not be empty")
	}
	for i, d := range cfg.Drivers {
		if d == "" {
			return fmt.Errorf("drivers[%d] is empty", i)
		}
	}
	for i, n := range cfg.Interfaces {
		if n == "" {
			return fmt.Errorf("interfaces[%d] is empty", i)
		}
	}
	return nil
}
