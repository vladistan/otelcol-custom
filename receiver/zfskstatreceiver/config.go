// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config defines the settings of the ZFS kstat receiver.
type Config struct {
	// CollectionInterval is the time between two reads of the kstat files.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// KstatPath is the directory that holds one directory per pool and the arcstats file.
	KstatPath string `mapstructure:"kstat_path"`
	// Pools limits the receiver to these pools; empty means every pool found.
	Pools []string `mapstructure:"pools"`
	// ExcludePools removes pools from the set selected by Pools.
	ExcludePools []string `mapstructure:"exclude_pools"`
	// ArcStats turns the ARC metrics on or off.
	ArcStats bool `mapstructure:"arcstats"`
}

var _ component.Config = (*Config)(nil)

// Validate checks the interval, the path and the pool names.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.KstatPath == "" {
		return errors.New("kstat_path must be set")
	}
	for i, pool := range cfg.Pools {
		if pool == "" {
			return fmt.Errorf("pools entry %d is empty", i)
		}
	}
	for i, pool := range cfg.ExcludePools {
		if pool == "" {
			return fmt.Errorf("exclude_pools entry %d is empty", i)
		}
	}
	return nil
}
