// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ethtoolreceiver

import (
	"errors"
	"fmt"
	"regexp"
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
	// Statistics selects driver statistics by name for network.interface.statistic.
	// With no entry the receiver emits no such series.
	Statistics []Statistic `mapstructure:"statistics"`
	// MaxStatistics caps the network.interface.statistic series of one scrape.
	MaxStatistics int `mapstructure:"max_statistics"`
}

// Statistic is one entry of the statistics list.
type Statistic struct {
	// Match is a regular expression tested against the full statistic name.
	Match string `mapstructure:"match"`
	// Type is sum for a counter or gauge for a value that moves both ways.
	Type string `mapstructure:"type"`
}

// Statistic types.
const (
	typeSum   = "sum"
	typeGauge = "gauge"
)

var _ component.Config = (*Config)(nil)

// Validate rejects an interval under one second, empty driver or interface names,
// a statistics entry with a bad pattern or type, and a non-positive cap.
func (cfg *Config) Validate() error {
	if cfg.MaxStatistics <= 0 {
		return errors.New("max_statistics must be positive")
	}
	for i, st := range cfg.Statistics {
		if _, err := regexp.Compile(st.Match); err != nil {
			return fmt.Errorf("statistics[%d].match: %w", i, err)
		}
		if st.Match == "" {
			return fmt.Errorf("statistics[%d].match is empty", i)
		}
		if st.Type != typeSum && st.Type != typeGauge {
			return fmt.Errorf("statistics[%d].type must be %q or %q, got %q", i, typeSum, typeGauge, st.Type)
		}
	}
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
