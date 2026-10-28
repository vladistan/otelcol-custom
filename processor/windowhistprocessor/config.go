// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	defaultScrapeInterval = 10 * time.Second
	defaultSendInterval   = 60 * time.Second
	defaultMaxSize        = 16
	minMaxSize            = 2
	maxMaxSize            = 160
)

// Config holds the global intervals and the per-group settings.
type Config struct {
	ScrapeInterval time.Duration          `mapstructure:"scrape_interval"`
	SendInterval   time.Duration          `mapstructure:"send_interval"`
	Groups         map[string]GroupConfig `mapstructure:"groups"`
}

// GroupConfig overrides the global settings for one catalogue group. Zero values inherit.
type GroupConfig struct {
	ScrapeInterval time.Duration `mapstructure:"scrape_interval"`
	SendInterval   time.Duration `mapstructure:"send_interval"`
	Mode           string        `mapstructure:"mode"`
	Temporality    string        `mapstructure:"temporality"`
	MaxSize        int           `mapstructure:"max_size"`
	KeepCounter    *bool         `mapstructure:"keep_counter"`
	Metrics        []string      `mapstructure:"metrics"`
}

// GroupSettings is a group with every default applied.
type GroupSettings struct {
	ScrapeInterval time.Duration
	SendInterval   time.Duration
	Mode           string
	Temporality    string
	MaxSize        int
	KeepCounter    bool
	Metrics        []string
}

var _ component.Config = (*Config)(nil)

func createDefaultConfig() component.Config {
	return &Config{
		ScrapeInterval: defaultScrapeInterval,
		SendInterval:   defaultSendInterval,
	}
}

// Resolve returns the effective settings of a catalogue group.
func (cfg *Config) Resolve(name string) (GroupSettings, error) {
	entry, ok := catalogue[name]
	if !ok {
		return GroupSettings{}, fmt.Errorf("unknown group %q", name)
	}
	group := cfg.Groups[name]

	s := GroupSettings{
		ScrapeInterval: firstNonZero(group.ScrapeInterval, cfg.ScrapeInterval),
		SendInterval:   firstNonZero(group.SendInterval, cfg.SendInterval),
		Mode:           entry.Mode,
		MaxSize:        defaultMaxSize,
		KeepCounter:    entry.CounterSourced,
		Metrics:        entry.Metrics,
	}
	if group.Mode != "" {
		s.Mode = group.Mode
	}
	if s.Mode == ModeHistogram {
		s.Temporality = TemporalityDelta
	}
	if group.Temporality != "" {
		s.Temporality = group.Temporality
	}
	if group.MaxSize != 0 {
		s.MaxSize = group.MaxSize
	}
	if group.KeepCounter != nil {
		s.KeepCounter = *group.KeepCounter
	}
	if group.Metrics != nil {
		s.Metrics = group.Metrics
	}
	return s, nil
}

// Validate rejects the configurations listed in SPARC 05 FR-045a.
func (cfg *Config) Validate() error {
	errs := checkIntervals("global", cfg.ScrapeInterval, cfg.SendInterval)

	names := make([]string, 0, len(cfg.Groups))
	for name := range cfg.Groups {
		names = append(names, name)
	}
	sort.Strings(names)

	scraperInterval := map[string]time.Duration{}
	scraperGroup := map[string]string{}
	for _, name := range names {
		if _, ok := catalogue[name]; !ok {
			errs = append(errs, fmt.Errorf("unknown group %q", name))
			continue
		}
		s, _ := cfg.Resolve(name)
		errs = append(errs, checkGroup(name, cfg.Groups[name], s)...)
		if s.ScrapeInterval <= 0 {
			continue
		}
		for _, scraper := range catalogue[name].Scrapers {
			prev, seen := scraperInterval[scraper]
			if !seen {
				scraperInterval[scraper] = s.ScrapeInterval
				scraperGroup[scraper] = name
				continue
			}
			if prev != s.ScrapeInterval {
				errs = append(errs, fmt.Errorf(
					"scraper %q has two scrape intervals: group %q uses %v, group %q uses %v",
					scraper, scraperGroup[scraper], prev, name, s.ScrapeInterval))
			}
		}
	}
	return errors.Join(errs...)
}

func checkGroup(name string, raw GroupConfig, s GroupSettings) []error {
	var errs []error
	errs = append(errs, checkIntervals("group "+name, s.ScrapeInterval, s.SendInterval)...)
	if s.Mode != ModeHistogram && s.Mode != ModePlain {
		errs = append(errs, fmt.Errorf("group %q: mode %q must be %q or %q", name, s.Mode, ModeHistogram, ModePlain))
	}
	switch {
	case raw.Temporality != TemporalityDelta && raw.Temporality != TemporalityCumulative && raw.Temporality != "":
		errs = append(errs, fmt.Errorf("group %q: temporality %q must be %q or %q",
			name, raw.Temporality, TemporalityDelta, TemporalityCumulative))
	case raw.Temporality != "" && s.Mode == ModePlain:
		errs = append(errs, fmt.Errorf("group %q: temporality is not allowed on a plain group", name))
	}
	if raw.MaxSize != 0 && (raw.MaxSize < minMaxSize || raw.MaxSize > maxMaxSize) {
		errs = append(errs, fmt.Errorf("group %q: max_size %d must be in %d..%d",
			name, raw.MaxSize, minMaxSize, maxMaxSize))
	}
	return errs
}

func checkIntervals(label string, scrape, send time.Duration) []error {
	switch {
	case scrape <= 0 || send <= 0:
		return []error{fmt.Errorf("%s: scrape_interval and send_interval must be positive", label)}
	case send < scrape:
		return []error{fmt.Errorf("%s: send_interval %v is smaller than scrape_interval %v", label, send, scrape)}
	case send%scrape != 0:
		return []error{fmt.Errorf("%s: send_interval %v is not a whole multiple of scrape_interval %v", label, send, scrape)}
	}
	return nil
}

func firstNonZero(vals ...time.Duration) time.Duration {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
