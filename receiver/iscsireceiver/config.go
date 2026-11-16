// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
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
}

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
	return nil
}
