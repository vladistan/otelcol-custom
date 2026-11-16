// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"go.opentelemetry.io/collector/component"
)

const defaultMaxCatchup = 72 * time.Hour

// Config lists the accounts and regions to poll and the tiers to read in each.
type Config struct {
	// StorageID names a storage extension that keeps the end of the last window; without it the state is lost at restart.
	StorageID *component.ID `mapstructure:"storage"`
	// MaxCatchup bounds how far back missed windows are read after a stop.
	MaxCatchup time.Duration   `mapstructure:"max_catchup"`
	Accounts   []AccountConfig `mapstructure:"accounts"`
	// Tiers are keyed by name; every tier is read for every account and region.
	Tiers map[string]TierConfig `mapstructure:"tiers"`
}

// AccountConfig is one AWS account: the role to assume, if any, and the regions to read.
type AccountConfig struct {
	Name       string   `mapstructure:"name"`
	RoleARN    string   `mapstructure:"role_arn"`
	ExternalID string   `mapstructure:"external_id"`
	Regions    []string `mapstructure:"regions"`
}

// TierConfig sets the cadence, statistics and series limit of one tier.
type TierConfig struct {
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	Period             time.Duration `mapstructure:"period"`
	Delay              time.Duration `mapstructure:"delay"`
	// Stats are CloudWatch statistic names such as Average or p50; they are never defaulted.
	Stats []string `mapstructure:"stats"`
	// Limit is the most series read per account and region; the rest are counted, not read.
	Limit int `mapstructure:"limit"`
	// Namespaces maps a namespace to the metric names to read; an empty list reads the whole namespace.
	Namespaces map[string][]string `mapstructure:"namespaces"`
}

var _ component.Config = (*Config)(nil)

// Validate checks accounts, regions and every tier.
func (c *Config) Validate() error {
	if c.MaxCatchup < 0 {
		return errors.New("max_catchup must not be negative")
	}
	if len(c.Accounts) == 0 {
		return errors.New("accounts must not be empty")
	}
	names := map[string]bool{}
	for i, a := range c.Accounts {
		if err := a.validate(i, names); err != nil {
			return err
		}
	}
	if len(c.Tiers) == 0 {
		return errors.New("tiers must not be empty")
	}
	for _, name := range slices.Sorted(maps.Keys(c.Tiers)) {
		if err := c.Tiers[name].validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (a AccountConfig) validate(index int, seen map[string]bool) error {
	if a.Name == "" {
		return fmt.Errorf("account %d: name must be set", index)
	}
	if seen[a.Name] {
		return fmt.Errorf("duplicate account name %q", a.Name)
	}
	seen[a.Name] = true
	if len(a.Regions) == 0 {
		return fmt.Errorf("account %q: regions must not be empty", a.Name)
	}
	regions := map[string]bool{}
	for i, r := range a.Regions {
		if r == "" {
			return fmt.Errorf("account %q: region %d is empty", a.Name, i)
		}
		if regions[r] {
			return fmt.Errorf("account %q: duplicate region %q", a.Name, r)
		}
		regions[r] = true
	}
	if a.ExternalID != "" && a.RoleARN == "" {
		return fmt.Errorf("account %q: external_id needs role_arn", a.Name)
	}
	return nil
}

func (t TierConfig) validate(name string) error {
	switch {
	case t.Period <= 0:
		return fmt.Errorf("tier %q: period must be positive", name)
	case t.CollectionInterval <= 0:
		return fmt.Errorf("tier %q: collection_interval must be positive", name)
	case t.Period%time.Second != 0:
		return fmt.Errorf("tier %q: period must be a whole number of seconds", name)
	case !cloudWatchPeriod(t.Period):
		return fmt.Errorf("tier %q: period must be 1, 5, 10 or 30 seconds, or a multiple of 60 seconds", name)
	case t.CollectionInterval < t.Period:
		return fmt.Errorf("tier %q: collection_interval must not be below period", name)
	case t.CollectionInterval%t.Period != 0:
		return fmt.Errorf("tier %q: collection_interval must be a multiple of period", name)
	case t.Delay < 0:
		return fmt.Errorf("tier %q: delay must not be negative", name)
	case t.Limit <= 0:
		return fmt.Errorf("tier %q: limit must be positive", name)
	case len(t.Stats) == 0:
		return fmt.Errorf("tier %q: stats must not be empty", name)
	case len(t.Namespaces) == 0:
		return fmt.Errorf("tier %q: namespaces must not be empty", name)
	}
	stats := map[string]bool{}
	for i, s := range t.Stats {
		if s == "" {
			return fmt.Errorf("tier %q: stat %d is empty", name, i)
		}
		if stats[s] {
			return fmt.Errorf("tier %q: duplicate stat %q", name, s)
		}
		stats[s] = true
	}
	for ns, metrics := range t.Namespaces {
		if ns == "" {
			return fmt.Errorf("tier %q: namespace name is empty", name)
		}
		seen := map[string]bool{}
		for i, m := range metrics {
			if m == "" {
				return fmt.Errorf("tier %q: namespace %q: metric name %d is empty", name, ns, i)
			}
			if seen[m] {
				return fmt.Errorf("tier %q: namespace %q: duplicate metric name %q", name, ns, m)
			}
			seen[m] = true
		}
	}
	return nil
}

// cloudWatchPeriod reports whether CloudWatch accepts the period: 1, 5, 10 or
// 30 seconds, or a multiple of 60 seconds. The caller has checked whole seconds.
func cloudWatchPeriod(p time.Duration) bool {
	switch p {
	case time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second:
		return true
	}
	return p%time.Minute == 0
}
