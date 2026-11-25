// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"errors"
	"fmt"
	"regexp"
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

	// ContainerIdentifier is a field that names the container of an entry (e.g.,
	// "body.CONTAINER_ID"). An entry that has it is grouped by that value, so it never
	// joins an entry of another container or of a non-container source that shares
	// the same source identifier. Empty turns this off.
	// Default: "body.CONTAINER_ID"
	ContainerIdentifier string `mapstructure:"container_identifier"`

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

	// StripANSI removes ANSI colour codes from the combine field and from a plain string body.
	// Default: true
	StripANSI bool `mapstructure:"strip_ansi"`

	// DropBlank drops a record whose combine field or plain string body is only white space or
	// ANSI codes, written as text or as escapes (a bare "\r\n" record).
	// Default: true
	DropBlank bool `mapstructure:"drop_blank"`

	// RequireAttribute turns on gluing of wrapped console fragments (see glue.go). It names a
	// record attribute, set to true as a bool or as the text "true", that marks the records to
	// glue. A record without it passes through untouched and neither ends nor joins a sequence.
	// SourceIdentifier then names the node: "resource.<name>" reads a resource attribute and
	// "attributes.<name>" a record attribute whose name may hold dots.
	// Default: "" (off)
	RequireAttribute string `mapstructure:"require_attribute"`

	// NeverGluePatterns are regexes on the text. A record that matches one is a standalone line:
	// it ends the open sequence of its node and is passed on alone. Glue mode only.
	NeverGluePatterns []string `mapstructure:"never_glue_patterns"`

	// EmptyPatterns are regexes on the text. A record that matches one (or has no text) ends the
	// open sequence of its node and is dropped, after the glue. Glue mode only.
	EmptyPatterns []string `mapstructure:"empty_patterns"`

	// MinPreviousLength is the length a fragment must reach for the next record to continue it.
	// A wrapped line breaks at a width, so a short fragment is the last one of its line.
	// Glue mode only. Default: 0 (any untagged record continues an open sequence)
	MinPreviousLength int `mapstructure:"min_previous_length"`

	// FragmentSuffix is the end the console puts on every fragment. The glue removes it from
	// every fragment but the last. Glue mode only. Default: "" (nothing removed)
	FragmentSuffix string `mapstructure:"fragment_suffix"`

	// MaxTextSize is the size in bytes at which a glued sequence is passed on. Glue mode only.
	// Default: 65536
	MaxTextSize int `mapstructure:"max_text_size"`
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
	return cfg.validateGlue()
}

func (cfg *Config) validateGlue() error {
	if cfg.RequireAttribute == "" {
		if len(cfg.NeverGluePatterns) > 0 || len(cfg.EmptyPatterns) > 0 || cfg.MinPreviousLength != 0 || cfg.FragmentSuffix != "" {
			return errors.New("never_glue_patterns, empty_patterns, min_previous_length and fragment_suffix need require_attribute")
		}
		return nil
	}
	if cfg.MinPreviousLength < 0 {
		return errors.New("min_previous_length must not be negative")
	}
	if cfg.MaxTextSize <= 0 {
		return errors.New("max_text_size must be positive")
	}
	for name, list := range map[string][]string{"never_glue_patterns": cfg.NeverGluePatterns, "empty_patterns": cfg.EmptyPatterns} {
		for i, expr := range list {
			if _, err := regexp.Compile(expr); err != nil {
				return fmt.Errorf("%s[%d]: %w", name, i, err)
			}
		}
	}
	return nil
}
