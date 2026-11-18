// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	defaultInterval = 24 * time.Hour
	minInterval     = time.Minute
	defaultDrives   = 64
	maxDrivesLimit  = 1024
)

// Config sets how often the facts are read and how many drives are reported.
type Config struct {
	// CollectionInterval is the time between two collections; the first one runs at start.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// MaxDrives caps the drive series of one collection.
	MaxDrives int `mapstructure:"max_drives"`
}

var _ component.Config = (*Config)(nil)

// Validate rejects an interval under one minute and a drive cap outside 1..1024.
func (c *Config) Validate() error {
	if c.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if c.CollectionInterval < minInterval {
		return errors.New("collection_interval must be at least 1 minute")
	}
	if c.MaxDrives < 1 || c.MaxDrives > maxDrivesLimit {
		return errors.New("max_drives must be between 1 and 1024")
	}
	return nil
}
