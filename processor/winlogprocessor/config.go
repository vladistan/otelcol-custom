// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import "go.opentelemetry.io/collector/component"

// Config has no options yet; the step that builds the Windows event log processor adds them.
type Config struct{}

func (cfg *Config) Validate() error {
	return nil
}

var _ component.Config = (*Config)(nil)
