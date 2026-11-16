// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package journaldprocessor

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
)

// Config selects which journald mappings the processor applies.
type Config struct {
	// SetResource maps _HOSTNAME, _MACHINE_ID, _PID, _COMM and _SYSTEMD_UNIT to resource attributes.
	// An attribute the resource already has is left as it is.
	SetResource bool `mapstructure:"set_resource"`

	// MapSeverity maps PRIORITY 0-7 to severity.
	MapSeverity bool `mapstructure:"map_severity"`

	// BodyFromMessage replaces a map body with its MESSAGE string.
	BodyFromMessage bool `mapstructure:"body_from_message"`

	// SplitResources moves records with different resource tuples into separate ResourceLogs.
	SplitResources bool `mapstructure:"split_resources"`

	// KeepFields stores the unmapped journald fields under the journald.fields attribute.
	KeepFields bool `mapstructure:"keep_fields"`

	// MapExtended also maps _EXE, _CMDLINE, _UID, _GID and _SYSTEMD_CGROUP to resource
	// attributes, and seven more fields to log attributes. Needs set_resource.
	MapExtended bool `mapstructure:"map_extended"`

	// SkipWhenResourceHas lists resource attributes. A resource that carries any of them
	// gets no process.pid, process.command, service.name or container.id from the journal.
	SkipWhenResourceHas []string `mapstructure:"skip_when_resource_has"`

	// DropUnmappedUnderscore removes the keys that start with _ from a map body that has no MESSAGE.
	DropUnmappedUnderscore bool `mapstructure:"drop_unmapped_underscore"`
}

// Validate rejects the keys that act on resource attributes when set_resource is off.
func (cfg *Config) Validate() error {
	if cfg.SplitResources && !cfg.SetResource {
		return errors.New("split_resources requires set_resource")
	}
	if cfg.MapExtended && !cfg.SetResource {
		return errors.New("map_extended requires set_resource")
	}
	if len(cfg.SkipWhenResourceHas) > 0 && !cfg.SetResource {
		return errors.New("skip_when_resource_has requires set_resource")
	}
	for i, name := range cfg.SkipWhenResourceHas {
		if name == "" {
			return fmt.Errorf("skip_when_resource_has[%d] is empty", i)
		}
	}
	return nil
}

var _ component.Config = (*Config)(nil)
