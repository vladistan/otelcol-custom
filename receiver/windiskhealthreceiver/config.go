// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config sets how the receiver reads disks.
type Config struct {
	// CollectionInterval is the time between reads of the disks.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`

	// CommandTimeout is the time one command to one disk may take.
	CommandTimeout time.Duration `mapstructure:"command_timeout"`

	// Include lists regular expressions on the disk name (PhysicalDrive0); an empty list reads every disk.
	Include []string `mapstructure:"include"`

	// Exclude lists regular expressions on the disk name of disks to leave out.
	Exclude []string `mapstructure:"exclude"`

	// MaxDevices caps the disks read in one interval.
	MaxDevices int `mapstructure:"max_devices"`
}

var _ component.Config = (*Config)(nil)

// minCommandTimeout keeps the wait of a control call above the resolution of the system wait.
const minCommandTimeout = 100 * time.Millisecond

func (cfg *Config) Validate() error {
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.CommandTimeout <= 0 {
		return errors.New("command_timeout must be positive")
	}
	if cfg.CommandTimeout < minCommandTimeout {
		return fmt.Errorf("command_timeout must be at least %s", minCommandTimeout)
	}
	if cfg.CommandTimeout > cfg.CollectionInterval {
		return errors.New("command_timeout must not exceed collection_interval")
	}
	if _, err := newSelector(cfg.Include, cfg.Exclude); err != nil {
		return err
	}
	if cfg.MaxDevices <= 0 {
		return errors.New("max_devices must be positive")
	}
	return nil
}

// selector applies include and exclude to disk names.
type selector struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

func newSelector(include, exclude []string) (selector, error) {
	inc, err := compileAll("include", include)
	if err != nil {
		return selector{}, err
	}
	exc, err := compileAll("exclude", exclude)
	if err != nil {
		return selector{}, err
	}
	return selector{include: inc, exclude: exc}, nil
}

func compileAll(field string, exprs []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for i, expr := range exprs {
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func (s selector) filter(names []string) []string {
	out := []string{}
	for _, name := range names {
		if len(s.include) > 0 && !matchesAny(s.include, name) {
			continue
		}
		if matchesAny(s.exclude, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func matchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
