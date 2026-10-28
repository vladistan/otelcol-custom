// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/component"
)

// Config selects which journald mappings the processor applies.
type Config struct {
	// SetResource maps _HOSTNAME, _MACHINE_ID, _PID, _COMM and _SYSTEMD_UNIT to resource attributes.
	SetResource bool `mapstructure:"set_resource"`

	// MapSeverity maps PRIORITY 0-7 to severity.
	MapSeverity bool `mapstructure:"map_severity"`

	// BodyFromMessage replaces a map body with its MESSAGE string.
	BodyFromMessage bool `mapstructure:"body_from_message"`

	// SplitResources moves records with different resource tuples into separate ResourceLogs.
	SplitResources bool `mapstructure:"split_resources"`

	// KeepFields stores the unmapped journald fields under the journald.fields attribute.
	KeepFields bool `mapstructure:"keep_fields"`
}

func (cfg *Config) Validate() error {
	if cfg.SplitResources && !cfg.SetResource {
		return errors.New("split_resources requires set_resource")
	}
	return nil
}

var _ component.Config = (*Config)(nil)
