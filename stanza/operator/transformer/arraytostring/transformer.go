// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package arraytostring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

// Transformer converts array fields to joined strings.
// If the field is not an array, it passes through unchanged.
type Transformer struct {
	helper.TransformerOperator
	Field     entry.Field
	Delimiter string
}

// Process will process an entry with the array_to_string transformation.
func (t *Transformer) Process(ctx context.Context, entry *entry.Entry) error {
	return t.ProcessWith(ctx, entry, t.Transform)
}

// ProcessBatch processes a batch of entries with the array_to_string transformation.
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

// Transform converts an array field to a string.
// It handles two types of arrays:
// 1. Byte arrays ([]interface{} of int/float64 values 0-255): converts to string directly
// 2. String arrays (multi-value fields): joins elements with the configured delimiter
// If the field doesn't exist or is not an array, it passes through unchanged.
func (t *Transformer) Transform(e *entry.Entry) error {
	val, exist := t.Field.Get(e)
	if !exist {
		// Field doesn't exist, pass through unchanged
		return nil
	}

	// Check if the value is an array ([]interface{})
	arr, isArray := val.([]interface{})
	if !isArray {
		// Not an array, pass through unchanged
		return nil
	}

	if len(arr) == 0 {
		return t.Field.Set(e, "")
	}

	// Check if this looks like a byte array (journald stores binary MESSAGE as []uint8)
	// Byte arrays come from JSON as []interface{} of float64 values (JSON numbers)
	if isByteArray(arr) {
		// Convert numeric array to string (each number is a byte value)
		bytes := make([]byte, len(arr))
		for i, v := range arr {
			switch num := v.(type) {
			case float64:
				bytes[i] = byte(num)
			case int:
				bytes[i] = byte(num)
			case int64:
				bytes[i] = byte(num)
			case uint8:
				bytes[i] = num
			}
		}
		return t.Field.Set(e, string(bytes))
	}

	// Multi-value string array - join with delimiter
	parts := make([]string, 0, len(arr))
	for _, v := range arr {
		switch val := v.(type) {
		case string:
			parts = append(parts, val)
		case []byte:
			parts = append(parts, string(val))
		default:
			parts = append(parts, fmt.Sprintf("%v", val))
		}
	}

	joined := strings.Join(parts, t.Delimiter)
	return t.Field.Set(e, joined)
}

// isByteArray checks if the array appears to be a byte array (all numeric values 0-255).
// journald stores binary MESSAGE fields as byte arrays, which JSON represents as arrays
// of numbers (float64 in Go's json unmarshal).
func isByteArray(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	for _, v := range arr {
		switch num := v.(type) {
		case float64:
			if num < 0 || num > 255 || num != float64(byte(num)) {
				return false
			}
		case int:
			if num < 0 || num > 255 {
				return false
			}
		case int64:
			if num < 0 || num > 255 {
				return false
			}
		case uint8:
			// Already a byte, valid
		default:
			// Not a numeric type, not a byte array
			return false
		}
	}
	return true
}
