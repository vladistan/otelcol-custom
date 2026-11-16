// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/component"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// Config sets how the receiver reads disks.
type Config struct {
	// CollectionInterval is the time between reads of the disks.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`

	// CommandTimeout is the time one command to one disk may take.
	CommandTimeout time.Duration `mapstructure:"command_timeout"`

	// Include lists regular expressions on the kernel name; an empty list reads every discovered device.
	Include []string `mapstructure:"include"`

	// Exclude lists regular expressions on the kernel name of devices to leave out.
	Exclude []string `mapstructure:"exclude"`

	// SkipIfStandby leaves a disk in standby alone: CHECK POWER MODE runs, no SMART command follows.
	SkipIfStandby bool `mapstructure:"skip_if_standby"`

	// Attributes selects the SATA attributes behind hw.physical_disk.smart: "default" (20 ids), "all",
	// or a list of ids; an id is a number, or a string of digits. Unset means "default".
	Attributes any `mapstructure:"attributes"`

	// ValueTypes lists the values emitted for each attribute: normalized, worst, threshold, raw.
	ValueTypes []string `mapstructure:"value_types"`

	// MaxDevices caps the devices read in one interval.
	MaxDevices int `mapstructure:"max_devices"`
}

var _ component.Config = (*Config)(nil)

var validValueTypes = []string{hwschema.ValueNormalized, hwschema.ValueWorst, hwschema.ValueThreshold, hwschema.ValueRaw}

// Validate checks the configuration.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.CollectionInterval < time.Second {
		return errors.New("collection_interval must be at least 1 second")
	}
	if cfg.CommandTimeout <= 0 {
		return errors.New("command_timeout must be positive")
	}
	if cfg.CommandTimeout > cfg.CollectionInterval {
		return errors.New("command_timeout must not exceed collection_interval")
	}
	for i, expr := range cfg.Include {
		if _, err := regexp.Compile(expr); err != nil {
			return fmt.Errorf("include[%d]: %w", i, err)
		}
	}
	for i, expr := range cfg.Exclude {
		if _, err := regexp.Compile(expr); err != nil {
			return fmt.Errorf("exclude[%d]: %w", i, err)
		}
	}
	if _, err := cfg.attributeSelection(); err != nil {
		return err
	}
	if len(cfg.ValueTypes) == 0 {
		return errors.New("value_types must not be empty")
	}
	seen := map[string]bool{}
	for _, v := range cfg.ValueTypes {
		if !slices.Contains(validValueTypes, v) {
			return fmt.Errorf("value_types: %q is not one of %v", v, validValueTypes)
		}
		if seen[v] {
			return fmt.Errorf("value_types: %q is listed twice", v)
		}
		seen[v] = true
	}
	if cfg.MaxDevices <= 0 {
		return errors.New("max_devices must be positive")
	}
	return nil
}

// attributeSelection is the set of attribute ids to emit.
type attributeSelection struct {
	all bool
	ids map[uint8]bool
}

func (s attributeSelection) wants(id uint8) bool { return s.all || s.ids[id] }

func (cfg *Config) attributeSelection() (attributeSelection, error) {
	const usage = "attributes must be \"default\", \"all\" or a list of ids from 0 to 255"
	switch v := cfg.Attributes.(type) {
	case nil:
		return defaultSelection(), nil
	case string:
		switch v {
		case "all":
			return attributeSelection{all: true}, nil
		case "default":
			return defaultSelection(), nil
		}
		return attributeSelection{}, fmt.Errorf("%s, got %q", usage, v)
	case []any:
		if len(v) == 0 {
			return attributeSelection{}, errors.New(usage)
		}
		ids := map[uint8]bool{}
		for _, item := range v {
			n, err := attributeID(item)
			if err != nil {
				return attributeSelection{}, fmt.Errorf("%s, got %v", usage, item)
			}
			ids[n] = true
		}
		return attributeSelection{ids: ids}, nil
	}
	return attributeSelection{}, errors.New(usage)
}

func defaultSelection() attributeSelection {
	ids := map[uint8]bool{}
	for _, id := range smartdecode.DefaultAttributeIDs() {
		ids[id] = true
	}
	return attributeSelection{ids: ids}
}

// attributeID reads one entry of the attributes list as an id from 0 to 255.
func attributeID(item any) (uint8, error) {
	var n uint64
	switch v := item.(type) {
	case string:
		parsed, err := strconv.ParseUint(v, 10, 8)
		if err != nil {
			return 0, err
		}
		n = parsed
	case int:
		if v < 0 || v > 255 {
			return 0, errors.New("out of range")
		}
		n = uint64(v)
	case int64:
		if v < 0 || v > 255 {
			return 0, errors.New("out of range")
		}
		n = uint64(v)
	case uint64:
		if v > 255 {
			return 0, errors.New("out of range")
		}
		n = v
	case float64:
		if v < 0 || v > 255 || v != float64(uint64(v)) {
			return 0, errors.New("out of range")
		}
		n = uint64(v)
	default:
		return 0, fmt.Errorf("unsupported type %T", item)
	}
	return uint8(n), nil
}
