// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"fmt"
	"path"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config selects the sensors to read and how often.
type Config struct {
	// CollectionInterval is the time between two reads.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// HwmonPath is the hwmon class directory. The thermal zones are read from
	// the sibling directory "thermal".
	HwmonPath string `mapstructure:"hwmon_path"`
	// Include and Exclude are globs on "chip:label"; an empty Include keeps every sensor.
	Include []string `mapstructure:"include"`
	Exclude []string `mapstructure:"exclude"`
	// Limits adds the max, crit, min and lcrit limits of each temperature sensor.
	Limits bool `mapstructure:"limits"`
	// Fans and Voltages turn the fan and voltage sensors on or off.
	Fans     bool `mapstructure:"fans"`
	Voltages bool `mapstructure:"voltages"`
	// ThermalZones adds the Linux thermal zones.
	ThermalZones bool `mapstructure:"thermal_zones"`
	// CPUCores adds the per-core CPU temperatures.
	CPUCores bool `mapstructure:"cpu_cores"`
}

var _ component.Config = (*Config)(nil)

// Validate rejects an interval under one second, an empty path and a bad glob.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.HwmonPath == "" {
		return errors.New("hwmon_path must not be empty")
	}
	if err := checkGlobs("include", cfg.Include); err != nil {
		return err
	}
	return checkGlobs("exclude", cfg.Exclude)
}

func checkGlobs(key string, globs []string) error {
	for i, g := range globs {
		if g == "" {
			return fmt.Errorf("%s[%d] is empty", key, i)
		}
		if _, err := path.Match(g, ""); err != nil {
			return fmt.Errorf("%s[%d] %q is not a valid glob: %w", key, i, g, err)
		}
	}
	return nil
}

// readOptions are the switches a source needs.
type readOptions struct {
	limits, fans, voltages, thermalZones, cpuCores bool
}

func (cfg *Config) options() readOptions {
	return readOptions{
		limits:       cfg.Limits,
		fans:         cfg.Fans,
		voltages:     cfg.Voltages,
		thermalZones: cfg.ThermalZones,
		cpuCores:     cfg.CPUCores,
	}
}
