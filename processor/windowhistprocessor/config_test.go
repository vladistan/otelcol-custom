// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"strings"
	"testing"
	"time"
)

func cfgWith(groups map[string]GroupConfig) *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Groups = groups
	return cfg
}

func wantErr(t *testing.T, cfg *Config, substr string) {
	t.Helper()
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() = nil, want error containing %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("Validate() = %q, want it to contain %q", err, substr)
	}
}

func TestSendIntervalMustBeWholeMultipleOfScrape(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{
		"cpu": {ScrapeInterval: 10 * time.Second, SendInterval: 25 * time.Second},
	})
	wantErr(t, cfg, "multiple")
}

func TestSendIntervalBelowScrapeRejected(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{
		"cpu": {ScrapeInterval: 30 * time.Second, SendInterval: 10 * time.Second},
	})
	wantErr(t, cfg, "smaller than scrape_interval")
}

func TestGlobalIntervalsMustBeConsistent(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.SendInterval = 25 * time.Second
	wantErr(t, cfg, "multiple")
}

func TestTwoScrapeIntervalsForOneScraperRejected(t *testing.T) {
	// cpu and plain are both fed by the cpu scraper.
	cfg := cfgWith(map[string]GroupConfig{
		"cpu":   {ScrapeInterval: 10 * time.Second},
		"plain": {ScrapeInterval: 60 * time.Second, SendInterval: 60 * time.Second},
	})
	wantErr(t, cfg, "scraper")
}

func TestSameScraperDifferentWindowsAllowed(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{
		"cpu":   {SendInterval: 120 * time.Second},
		"plain": {SendInterval: 60 * time.Second},
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestUnknownGroupRejected(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"nonesuch": {}})
	wantErr(t, cfg, "unknown group")
}

func TestMaxSizeRange(t *testing.T) {
	for _, size := range []int{-1, 1, 161} {
		cfg := cfgWith(map[string]GroupConfig{"cpu": {MaxSize: size}})
		wantErr(t, cfg, "max_size")
	}
	for _, size := range []int{0, 2, 16, 160} {
		cfg := cfgWith(map[string]GroupConfig{"cpu": {MaxSize: size}})
		if err := cfg.Validate(); err != nil {
			t.Errorf("max_size %d: Validate() = %v, want nil", size, err)
		}
	}
}

func TestTemporalityOnPlainGroupRejected(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"process": {Temporality: TemporalityDelta}})
	wantErr(t, cfg, "temporality")

	cfg = cfgWith(map[string]GroupConfig{"cpu": {Mode: ModePlain, Temporality: TemporalityCumulative}})
	wantErr(t, cfg, "temporality")
}

func TestUnknownModeAndTemporalityRejected(t *testing.T) {
	wantErr(t, cfgWith(map[string]GroupConfig{"cpu": {Mode: "sparkline"}}), "mode")
	wantErr(t, cfgWith(map[string]GroupConfig{"cpu": {Temporality: "rolling"}}), "temporality")
}

func TestGlobalSettingAppliesToEveryGroup(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"cpu": {}, "memory": {}, "process": {}})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	for name := range cfg.Groups {
		got, err := cfg.Resolve(name)
		if err != nil {
			t.Fatalf("Resolve(%s) = %v", name, err)
		}
		if got.ScrapeInterval != 10*time.Second || got.SendInterval != 60*time.Second {
			t.Errorf("%s resolved to %v/%v, want 10s/60s", name, got.ScrapeInterval, got.SendInterval)
		}
	}
}

func TestGroupOverrideWins(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{
		"filesystem": {ScrapeInterval: 15 * time.Minute, SendInterval: 15 * time.Minute, Mode: ModePlain},
		"cpu":        {},
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	fs, err := cfg.Resolve("filesystem")
	if err != nil {
		t.Fatal(err)
	}
	if fs.ScrapeInterval != 15*time.Minute || fs.SendInterval != 15*time.Minute {
		t.Errorf("filesystem = %v/%v, want 15m/15m", fs.ScrapeInterval, fs.SendInterval)
	}
	cpu, err := cfg.Resolve("cpu")
	if err != nil {
		t.Fatal(err)
	}
	if cpu.ScrapeInterval != 10*time.Second || cpu.SendInterval != 60*time.Second {
		t.Errorf("cpu = %v/%v, want 10s/60s", cpu.ScrapeInterval, cpu.SendInterval)
	}
}

func TestOverridingOnlyScrapeKeepsGlobalSend(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"cpu": {ScrapeInterval: 20 * time.Second}})
	got, err := cfg.Resolve("cpu")
	if err != nil {
		t.Fatal(err)
	}
	if got.ScrapeInterval != 20*time.Second || got.SendInterval != 60*time.Second {
		t.Errorf("cpu = %v/%v, want 20s/60s", got.ScrapeInterval, got.SendInterval)
	}
}

func TestResolveDefaults(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"network_rate": {}, "cpu": {}, "process": {}})

	rate, err := cfg.Resolve("network_rate")
	if err != nil {
		t.Fatal(err)
	}
	if rate.Mode != ModeHistogram || rate.Temporality != TemporalityDelta || rate.MaxSize != 16 || !rate.KeepCounter {
		t.Errorf("network_rate = %+v, want histogram/delta/16/keep_counter", rate)
	}

	cpu, err := cfg.Resolve("cpu")
	if err != nil {
		t.Fatal(err)
	}
	if cpu.KeepCounter {
		t.Error("cpu is not counter-sourced; keep_counter should default false")
	}

	proc, err := cfg.Resolve("process")
	if err != nil {
		t.Fatal(err)
	}
	if proc.Mode != ModePlain || proc.Temporality != "" {
		t.Errorf("process = %+v, want plain with no temporality", proc)
	}
}

func TestKeepCounterExplicitFalse(t *testing.T) {
	off := false
	cfg := cfgWith(map[string]GroupConfig{"network_rate": {KeepCounter: &off}})
	got, err := cfg.Resolve("network_rate")
	if err != nil {
		t.Fatal(err)
	}
	if got.KeepCounter {
		t.Error("explicit keep_counter=false was overridden by the default")
	}
}

func TestResolveUnknownGroup(t *testing.T) {
	_, err := createDefaultConfig().(*Config).Resolve("nonesuch")
	if err == nil || !strings.Contains(err.Error(), "unknown group") {
		t.Fatalf("Resolve(unknown) = %v, want unknown group error", err)
	}
}

func TestMetricListOverride(t *testing.T) {
	cfg := cfgWith(map[string]GroupConfig{"cpu": {Metrics: []string{"system.cpu.utilization", "custom.cpu"}}})
	got, err := cfg.Resolve("cpu")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) != 2 || got.Metrics[1] != "custom.cpu" {
		t.Errorf("metrics = %v, want the override list", got.Metrics)
	}
}

func TestCatalogueEntriesAreWellFormed(t *testing.T) {
	for name, entry := range catalogue {
		if len(entry.Scrapers) == 0 {
			t.Errorf("%s has no scraper", name)
		}
		if entry.Mode != ModeHistogram && entry.Mode != ModePlain {
			t.Errorf("%s has mode %q", name, entry.Mode)
		}
		if entry.CounterSourced && entry.Mode != ModeHistogram {
			t.Errorf("%s is counter-sourced but not a histogram group", name)
		}
	}
}
