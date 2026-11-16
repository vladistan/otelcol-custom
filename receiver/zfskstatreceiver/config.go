// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
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
	// Datasets turns the per-dataset I/O metrics on or off.
	Datasets bool `mapstructure:"datasets"`
	// MaxDatasets caps the datasets read per scrape, in sorted order; 0 means no cap.
	MaxDatasets int `mapstructure:"max_datasets"`
	// Source is auto, proc or sysctl; auto means sysctl on FreeBSD and proc elsewhere.
	// KstatPath applies to the proc source only.
	Source string `mapstructure:"source"`
}

const (
	sourceAuto   = "auto"
	sourceProc   = "proc"
	sourceSysctl = "sysctl"
)

var _ component.Config = (*Config)(nil)

// resolvedSource returns proc or sysctl; an unset source counts as auto.
func (cfg *Config) resolvedSource() string {
	if cfg.Source == sourceProc || cfg.Source == sourceSysctl {
		return cfg.Source
	}
	if runtime.GOOS == "freebsd" {
		return sourceSysctl
	}
	return sourceProc
}

// selected reports whether pools and exclude_pools let the pool through.
func (cfg *Config) selected(pool string) bool {
	if slices.Contains(cfg.ExcludePools, pool) {
		return false
	}
	return len(cfg.Pools) == 0 || slices.Contains(cfg.Pools, pool)
}

// Validate checks the interval, the path and the pool names.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	switch cfg.Source {
	case "", sourceAuto, sourceProc, sourceSysctl:
	default:
		return fmt.Errorf("source must be auto, proc or sysctl, got %q", cfg.Source)
	}
	if cfg.MaxDatasets < 0 {
		return errors.New("max_datasets must not be negative")
	}
	if cfg.KstatPath == "" && cfg.resolvedSource() == sourceProc {
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
