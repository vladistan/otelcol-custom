// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for the logrecombine processor.
type Config struct {
	// CombineField is the log record field to combine (e.g., "body.MESSAGE")
	// Default: "body.MESSAGE"
	CombineField string `mapstructure:"combine_field"`

	// SourceIdentifier is the field used to group log entries (e.g., "body._PID")
	// Entries with the same source identifier are candidates for recombination.
	// Default: "body._PID"
	SourceIdentifier string `mapstructure:"source_identifier"`

	// ExcludeAttribute is an attribute name that, when set to "true", causes the
	// log entry to pass through without recombination. Useful for containers that
	// emit single-line logs that should not be combined.
	// Default: "" (disabled)
	ExcludeAttribute string `mapstructure:"exclude_attribute"`

	// IsFirstEntryPattern is a regex pattern that marks the start of a new log entry.
	// Entries matching this pattern start a new combined entry; non-matching entries
	// are appended to the previous entry (if within the same source).
	// Default: "^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}" (ISO timestamp)
	IsFirstEntryPattern string `mapstructure:"is_first_entry_pattern"`

	// CombineWith is the delimiter used when combining entries.
	// Default: "\n"
	CombineWith string `mapstructure:"combine_with"`

	// GapTimeout is the maximum time between consecutive messages in a combined entry.
	// If more than GapTimeout passes since the last message, the buffer is flushed.
	// Default: 10ms
	GapTimeout time.Duration `mapstructure:"gap_timeout"`

	// MaxTimeout is the maximum total time a combined entry can span.
	// If more than MaxTimeout passes since the first message, the buffer is flushed.
	// Default: 80ms
	MaxTimeout time.Duration `mapstructure:"max_timeout"`

	// MaxBatchSize is the maximum number of entries to buffer per source identifier.
	// When exceeded, the buffer is flushed.
	// Default: 50
	MaxBatchSize int `mapstructure:"max_batch_size"`

	// MaxSources is the maximum number of unique source identifiers to track.
	// Oldest sources are flushed when exceeded to prevent memory growth.
	// Default: 1000
	MaxSources int `mapstructure:"max_sources"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.CombineField == "" {
		return errors.New("combine_field must be specified")
	}
	if cfg.SourceIdentifier == "" {
		return errors.New("source_identifier must be specified")
	}
	if cfg.IsFirstEntryPattern == "" {
		return errors.New("is_first_entry_pattern must be specified")
	}
	if cfg.GapTimeout <= 0 {
		return errors.New("gap_timeout must be positive")
	}
	if cfg.MaxTimeout <= 0 {
		return errors.New("max_timeout must be positive")
	}
	if cfg.MaxBatchSize <= 0 {
		return errors.New("max_batch_size must be positive")
	}
	if cfg.MaxSources <= 0 {
		return errors.New("max_sources must be positive")
	}
	return nil
}
