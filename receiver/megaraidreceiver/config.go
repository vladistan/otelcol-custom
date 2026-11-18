// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
)

const (
	defaultInterval      = time.Minute
	defaultSmartInterval = 15 * time.Minute
	minSmartInterval     = time.Minute
	defaultMaxPerScrape  = 200
	maxMaxPerScrape      = maxEvents
	defaultMinClass      = "info"
	defaultEventsEnabled = true
)

// Config is the megaraid receiver configuration.
type Config struct {
	// CollectionInterval is how often the controllers are read.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// Smart turns on SMART reads of SATA drives through the controller; off by default.
	Smart bool `mapstructure:"smart"`
	// SmartInterval is the least time between two SMART reads of one drive.
	SmartInterval time.Duration `mapstructure:"smart_interval"`
	// CreateNode creates /dev/megaraid_sas_ioctl_node when it is missing; needs CAP_MKNOD.
	CreateNode bool `mapstructure:"create_node"`
	// Events configures the controller event log, sent as logs.
	Events EventsConfig `mapstructure:"events"`
}

// EventsConfig configures the event log.
type EventsConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Storage names the file_storage extension that keeps the last read sequence number.
	Storage *component.ID `mapstructure:"storage"`
	// MinClass is the lowest class sent: debug, progress, info, warning, critical, fatal or dead.
	MinClass string `mapstructure:"min_class"`
	// MaxPerScrape bounds the events read in one scrape.
	MaxPerScrape int `mapstructure:"max_per_scrape"`

	// enabledByDefault is set when the config file did not mention events.enabled, so a missing
	// storage is a plain note and not an error.
	enabledByDefault bool
}

// Validate checks the configuration. A storage name that no extension answers is not an error
// here: the receiver logs it at start and keeps sending metrics.
func (c *Config) Validate() error {
	var errs []error
	if c.CollectionInterval < time.Second {
		errs = append(errs, errors.New("collection_interval must be at least 1 second"))
	}
	if c.Smart && c.SmartInterval < minSmartInterval {
		errs = append(errs, fmt.Errorf("smart_interval must be at least %s", minSmartInterval))
	}
	if _, ok := parseClass(c.Events.MinClass); !ok {
		errs = append(errs, fmt.Errorf("events.min_class %q is not one of debug, progress, info, warning, critical, fatal, dead", c.Events.MinClass))
	}
	if c.Events.MaxPerScrape < 1 || c.Events.MaxPerScrape > maxMaxPerScrape {
		errs = append(errs, fmt.Errorf("events.max_per_scrape must be between 1 and %d", maxMaxPerScrape))
	}
	return errors.Join(errs...)
}

// configAlias has no Unmarshal method, so decoding into it does not call Config.Unmarshal again.
type configAlias Config

// Unmarshal decodes the config and records whether events.enabled came from the file or from the
// default.
func (c *Config) Unmarshal(conf *confmap.Conf) error {
	if err := conf.Unmarshal((*configAlias)(c)); err != nil {
		return err
	}
	c.Events.enabledByDefault = !conf.IsSet("events::enabled")
	return nil
}
