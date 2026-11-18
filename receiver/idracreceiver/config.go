// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// Config lists the iDRAC targets to poll.
type Config struct {
	// CollectionInterval is the time between two scrapes of each target.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// MaxConcurrent is the most targets read at once.
	MaxConcurrent int            `mapstructure:"max_concurrent"`
	Targets       []TargetConfig `mapstructure:"targets"`
}

// TargetConfig is one iDRAC.
type TargetConfig struct {
	// Name becomes host.name of the resource: the host the iDRAC manages.
	Name string `mapstructure:"name"`
	// Address is the https URL of the iDRAC.
	Address  string              `mapstructure:"address"`
	Username string              `mapstructure:"username"`
	Password configopaque.String `mapstructure:"password"`
	// CAFile pins the CA of the iDRAC certificate.
	CAFile string `mapstructure:"ca_file"`
	// InsecureSkipVerify turns certificate verification off; it is an explicit opt-in.
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`
	// Resources selects what is read: system, thermal, power, storage, memory,
	// processors and bmc. The default is all of them.
	Resources []string `mapstructure:"resources"`
	// RequestTimeout limits one request; ScrapeTimeout limits the whole read.
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
	ScrapeTimeout  time.Duration `mapstructure:"scrape_timeout"`
	// StorageInterval is the time between two reads of the storage resources (controllers, volumes and
	// drives), which run apart from the other scrapes. It may equal collection_interval and may not be below it.
	// The default is 5m, or collection_interval when that is longer.
	StorageInterval time.Duration `mapstructure:"storage_interval"`
	// StorageMaxDrives is the most drives read in one storage read. The default is 256.
	StorageMaxDrives int `mapstructure:"storage_max_drives"`
}

var _ component.Config = (*Config)(nil)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultScrapeTimeout  = 30 * time.Second
	defaultMaxConcurrent  = 8

	defaultStorageInterval  = 5 * time.Minute
	defaultStorageMaxDrives = 256
)

// resolved returns a copy of the target with the defaults filled in.
func (t TargetConfig) resolved() TargetConfig {
	if len(t.Resources) == 0 {
		t.Resources = hwschema.RedfishResources()
	}
	if t.RequestTimeout == 0 {
		t.RequestTimeout = defaultRequestTimeout
	}
	if t.ScrapeTimeout == 0 {
		t.ScrapeTimeout = defaultScrapeTimeout
	}
	if t.StorageMaxDrives == 0 {
		t.StorageMaxDrives = defaultStorageMaxDrives
	}
	return t
}

// storageInterval is the time between two storage reads: the configured interval, else 5 minutes or the
// collection interval when that is longer.
func (t TargetConfig) storageInterval(collection time.Duration) time.Duration {
	if t.StorageInterval > 0 {
		return t.StorageInterval
	}
	return max(defaultStorageInterval, collection)
}

// Validate checks the interval and every target.
func (c *Config) Validate() error {
	switch {
	case c.CollectionInterval <= 0:
		return errors.New("collection_interval must be positive")
	case c.CollectionInterval < time.Second:
		return errors.New("collection_interval must be at least 1 second")
	case c.MaxConcurrent < 1:
		return errors.New("max_concurrent must be at least 1")
	case len(c.Targets) == 0:
		return errors.New("targets must not be empty")
	}
	seen := map[string]bool{}
	for i, t := range c.Targets {
		if err := t.resolved().validate(i, seen); err != nil {
			return err
		}
		if t.StorageInterval != 0 && t.StorageInterval < c.CollectionInterval {
			return fmt.Errorf("target %q: storage_interval must not be below collection_interval", t.Name)
		}
	}
	return nil
}

func (t TargetConfig) validate(index int, seen map[string]bool) error {
	switch {
	case t.Name == "":
		return fmt.Errorf("targets[%d]: name must be set", index)
	case seen[t.Name]:
		return fmt.Errorf("duplicate target name %q", t.Name)
	}
	seen[t.Name] = true
	if t.Address == "" {
		return fmt.Errorf("target %q: address must be set", t.Name)
	}
	if u, err := url.Parse(t.Address); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("target %q: address must be an https URL", t.Name)
	}
	switch {
	case t.Username == "":
		return fmt.Errorf("target %q: username must be set", t.Name)
	case t.Password == "":
		return fmt.Errorf("target %q: password must be set", t.Name)
	case t.RequestTimeout < 0:
		return fmt.Errorf("target %q: request_timeout must not be negative", t.Name)
	case t.ScrapeTimeout < t.RequestTimeout:
		return fmt.Errorf("target %q: scrape_timeout must not be below request_timeout", t.Name)
	case t.StorageInterval < 0:
		return fmt.Errorf("target %q: storage_interval must not be negative", t.Name)
	case t.StorageMaxDrives < 0:
		return fmt.Errorf("target %q: storage_max_drives must not be negative", t.Name)
	}
	for _, r := range t.Resources {
		if !hwschema.IsRedfishResource(r) {
			return fmt.Errorf("target %q: unknown resource %q", t.Name, r)
		}
	}
	return nil
}
