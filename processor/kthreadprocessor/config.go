// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"errors"
	"fmt"
	"strings"
)

const defaultAggregateName = "kernel_threads"

var defaultPatterns = []string{"kworker*", "ksoftirqd*", "migration*"}

// Config selects which processes count as kernel threads.
type Config struct {
	// Patterns are globs on process.executable.name; * matches any run of
	// characters, including "/", and ? matches one character.
	Patterns []string `mapstructure:"patterns"`
	// AggregateName is the process.executable.name of the aggregate resource.
	AggregateName string `mapstructure:"aggregate_name"`
}

// catchAllPatterns lists the patterns made only of * and ?, which match every
// process name (or every name of a fixed length).
func (cfg *Config) catchAllPatterns() []string {
	var out []string
	for _, pattern := range cfg.Patterns {
		if pattern != "" && strings.Trim(pattern, "*?") == "" {
			out = append(out, pattern)
		}
	}
	return out
}

// Validate rejects an empty pattern list, an empty pattern and an empty name.
func (cfg *Config) Validate() error {
	if len(cfg.Patterns) == 0 {
		return errors.New("patterns must not be empty")
	}
	for i, pattern := range cfg.Patterns {
		if pattern == "" {
			return fmt.Errorf("pattern %d is empty", i)
		}
	}
	if cfg.AggregateName == "" {
		return errors.New("aggregate_name must be set")
	}
	return nil
}
