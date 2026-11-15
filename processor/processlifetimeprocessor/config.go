// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package processlifetimeprocessor

// Config has no keys: the metric names and the formulas are fixed.
type Config struct{}

// Validate always passes; there is nothing to check.
func (*Config) Validate() error { return nil }
