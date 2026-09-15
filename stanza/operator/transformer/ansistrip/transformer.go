// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ansistrip

import (
	"context"
	"errors"
	"regexp"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

// ansiPattern matches ANSI escape sequences.
// Format: ESC [ <params> <command>
// - ESC is \x1b (escape character)
// - [ is the CSI (Control Sequence Introducer)
// - params are optional numbers and semicolons (e.g., "32", "1;31")
// - command is a single letter (e.g., 'm' for color/style)
//
// Examples matched:
// - \x1b[0m     (reset)
// - \x1b[31m    (red)
// - \x1b[1;32m  (bold green)
// - \x1b[38;5;208m (256-color orange)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Transformer strips ANSI escape sequences from string fields.
type Transformer struct {
	helper.TransformerOperator
	Field entry.Field
}

// Process will process an entry with the ansi_strip transformation.
func (t *Transformer) Process(ctx context.Context, entry *entry.Entry) error {
	return t.ProcessWith(ctx, entry, t.Transform)
}

// ProcessBatch processes a batch of entries with the ansi_strip transformation.
//
// Implemented via the per-entry ProcessWith rather than helper.TransformerOperator's
// ProcessBatchWithTransform: the latter was only added to the stanza helper package in
// a later release than the one some build variants pin, and this component is shared
// across variants pinning different stanza versions.
func (t *Transformer) ProcessBatch(ctx context.Context, entries []*entry.Entry) error {
	var errs error
	for _, e := range entries {
		errs = errors.Join(errs, t.Process(ctx, e))
	}
	return errs
}

// Transform removes ANSI escape sequences from the specified field.
// If the field doesn't exist or is not a string, it passes through unchanged.
func (t *Transformer) Transform(e *entry.Entry) error {
	val, exist := t.Field.Get(e)
	if !exist {
		// Field doesn't exist, pass through unchanged
		return nil
	}

	// Only process strings
	str, isString := val.(string)
	if !isString {
		// Not a string, pass through unchanged
		return nil
	}

	// Strip ANSI escape sequences
	cleaned := ansiPattern.ReplaceAllString(str, "")
	return t.Field.Set(e, cleaned)
}

// StripANSI removes all ANSI escape sequences from a string.
// Exported for testing and potential reuse.
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
