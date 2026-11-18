// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"fmt"
	"path"
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
	// DatasetInclude lists globs on <pool>/<dataset>; empty means every dataset. Applies with datasets.
	DatasetInclude []string `mapstructure:"dataset_include"`
	// DatasetExclude lists globs on <pool>/<dataset> that remove datasets the include list let through.
	DatasetExclude []string `mapstructure:"dataset_exclude"`
	// MaxDatasets caps the datasets read per scrape, in sorted order; 0 means no cap.
	MaxDatasets int `mapstructure:"max_datasets"`
	// Txg turns the zfs.pool.txg.* metrics on or off.
	Txg bool `mapstructure:"txg"`
	// Source is auto, proc or sysctl; auto means sysctl on FreeBSD and proc elsewhere.
	// KstatPath applies to the proc source only.
	Source string `mapstructure:"source"`
	// IoctlDevice is the ZFS control device the space series are read from with read-only ioctls;
	// empty turns the space series off.
	IoctlDevice string `mapstructure:"ioctl_device"`
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

// datasetSelected reports whether the include and exclude globs let the dataset through.
// A bad pattern never matches; Validate rejects it at load.
func (cfg *Config) datasetSelected(dataset string) bool {
	for _, pattern := range cfg.DatasetExclude {
		if ok, _ := path.Match(pattern, dataset); ok {
			return false
		}
	}
	if len(cfg.DatasetInclude) == 0 {
		return true
	}
	for _, pattern := range cfg.DatasetInclude {
		if ok, _ := path.Match(pattern, dataset); ok {
			return true
		}
	}
	return false
}

// Validate checks the interval, the path, the pool names and the dataset globs.
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
	for name, patterns := range map[string][]string{"dataset_include": cfg.DatasetInclude, "dataset_exclude": cfg.DatasetExclude} {
		for i, pattern := range patterns {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("%s entry %d: %w", name, i, err)
			}
		}
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
