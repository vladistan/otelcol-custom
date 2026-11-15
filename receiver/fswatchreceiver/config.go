// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	startAtEnd       = "end"
	startAtBeginning = "beginning"

	minBufferSize = 4096
	maxBufferSize = 1 << 20
)

var driveRoot = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

type Config struct {
	// Paths are the watched roots: absolute, with a drive letter or a UNC prefix.
	Paths []string `mapstructure:"paths"`
	// Exclude holds doublestar globs matched case-insensitively against the root-relative path.
	Exclude []string `mapstructure:"exclude"`
	// StartAt applies to a root with no saved sentinel: end emits no history, beginning emits every file.
	StartAt string `mapstructure:"start_at"`
	// StorageID names the storage extension that keeps one sentinel per root; empty keeps it in memory.
	StorageID *component.ID `mapstructure:"storage"`
	// Settle is how long a size must stay unchanged before the write-complete record goes out; 0 turns it off.
	Settle time.Duration `mapstructure:"settle"`
	// SettleTimeout is how long a file is watched for growth; then the write-complete record goes out without a final size.
	SettleTimeout  time.Duration `mapstructure:"settle_timeout"`
	MaxCatchup     time.Duration `mapstructure:"max_catchup"`
	BufferSize     int           `mapstructure:"buffer_size"`
	MaxRescans     int           `mapstructure:"max_rescans"`
	RescanInterval time.Duration `mapstructure:"rescan_interval"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	var errs []error
	errs = append(errs, cfg.validatePaths()...)
	if cfg.Settle < 0 {
		errs = append(errs, errors.New("settle must not be negative"))
	}
	if cfg.SettleTimeout < cfg.Settle {
		errs = append(errs, fmt.Errorf("settle_timeout (%s) must be at least settle (%s)", cfg.SettleTimeout, cfg.Settle))
	}
	if cfg.StartAt != startAtEnd && cfg.StartAt != startAtBeginning {
		errs = append(errs, fmt.Errorf("start_at must be %q or %q, got %q", startAtEnd, startAtBeginning, cfg.StartAt))
	}
	if cfg.BufferSize < minBufferSize || cfg.BufferSize > maxBufferSize {
		errs = append(errs, fmt.Errorf("buffer_size must be between %d and %d bytes", minBufferSize, maxBufferSize))
	}
	if cfg.MaxRescans < 1 {
		errs = append(errs, errors.New("max_rescans must be at least 1"))
	}
	if cfg.MaxCatchup < 0 {
		errs = append(errs, errors.New("max_catchup must not be negative"))
	}
	if cfg.RescanInterval < 0 {
		errs = append(errs, errors.New("rescan_interval must not be negative"))
	}
	if _, err := newMatcher(cfg.Exclude); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (cfg *Config) validatePaths() []error {
	if len(cfg.Paths) == 0 {
		return []error{errors.New("paths must hold at least one root")}
	}
	var errs []error
	normalizedRoots := make([]string, 0, len(cfg.Paths))
	for _, path := range cfg.Paths {
		if !driveRoot.MatchString(path) && !isUNC(path) {
			errs = append(errs, fmt.Errorf("path %q must be absolute: a drive letter or a UNC prefix", path))
			continue
		}
		if strings.ContainsAny(path, "*?[{") {
			errs = append(errs, fmt.Errorf("path %q holds a glob character", path))
			continue
		}
		normalizedRoots = append(normalizedRoots, normalizeRoot(path))
	}
	for i, a := range normalizedRoots {
		for _, b := range normalizedRoots[i+1:] {
			switch {
			case a == b:
				errs = append(errs, fmt.Errorf("duplicate root %q", a))
			case strings.HasPrefix(b, a+`\`):
				errs = append(errs, fmt.Errorf("root %q is nested in root %q", b, a))
			case strings.HasPrefix(a, b+`\`):
				errs = append(errs, fmt.Errorf("root %q is nested in root %q", a, b))
			}
		}
	}
	return errs
}
