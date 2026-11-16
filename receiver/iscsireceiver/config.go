// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config selects where the iSCSI classes are and how often they are read.
type Config struct {
	// CollectionInterval is the time between two reads.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// SysfsPath is the sysfs class directory holding iscsi_session and
	// iscsi_connection; a host mount in a container.
	SysfsPath string `mapstructure:"sysfs_path"`
	// LUNs adds one status, size and state series for each LUN of each session.
	LUNs bool `mapstructure:"luns"`
	// MaxLUNs caps the LUNs reported; the rest are left out and one info line says so. Each LUN is
	// seven points. It is checked only when LUNs is on.
	MaxLUNs int `mapstructure:"max_luns"`
}

const (
	defaultMaxLUNs = 256
	maxMaxLUNs     = 4096
)

var _ component.Config = (*Config)(nil)

// Validate rejects an interval under one second and an empty path.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.SysfsPath == "" {
		return errors.New("sysfs_path must not be empty")
	}
	if cfg.LUNs && (cfg.MaxLUNs < 1 || cfg.MaxLUNs > maxMaxLUNs) {
		return fmt.Errorf("max_luns must be between 1 and %d", maxMaxLUNs)
	}
	return nil
}
