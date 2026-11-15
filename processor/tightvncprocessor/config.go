// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config works without any setting; the defaults fit a TightVNC server logging at level 9.
type Config struct {
	// TimeZone reads the log's zoneless timestamps: Local is the host the log was written on, UTC the other choice.
	TimeZone string `mapstructure:"time_zone"`
	// DropPrefixes lists the message prefixes of unmarked lines to drop. A configured list replaces the default.
	DropPrefixes []string `mapstructure:"drop_prefixes"`
}

func (cfg *Config) Validate() error {
	var errs []error
	if _, err := cfg.location(); err != nil {
		errs = append(errs, err)
	}
	// An empty prefix matches every message, so it would drop every unmarked line.
	if slices.Contains(cfg.DropPrefixes, "") {
		errs = append(errs, errors.New("drop_prefixes must not hold an empty entry"))
	}
	return errors.Join(errs...)
}

// location accepts only the two zones a Windows host has without zone data; the binary does not
// embed a tz database, which would add about 450 KB.
func (cfg *Config) location() (*time.Location, error) {
	switch cfg.TimeZone {
	case "Local":
		return time.Local, nil
	case "UTC":
		return time.UTC, nil
	}
	return nil, fmt.Errorf("time_zone %q is not supported; only Local and UTC are supported", cfg.TimeZone)
}

var _ component.Config = (*Config)(nil)
