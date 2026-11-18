// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ksmreceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config holds the scrape interval and the directory of the KSM files; Validate rejects an interval
// under one second and an empty path.
type Config struct {
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	KsmPath            string        `mapstructure:"ksm_path"`
}

var _ component.Config = (*Config)(nil)

// Validate returns the first setting that is out of range.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.KsmPath == "" {
		return errors.New("ksm_path must be set")
	}
	return nil
}
