// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

// Config has no keys: the processor has one fixed rule.
type Config struct{}

// Validate always passes; there is nothing to check.
func (*Config) Validate() error { return nil }
