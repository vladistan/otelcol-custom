// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"strings"
	"testing"
	"time"
)

func validConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Paths = []string{`D:\Projects`}
	return cfg
}

func TestDefaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	if cfg.StartAt != "end" || cfg.MaxCatchup != 168*time.Hour || cfg.SettleTimeout != 10*time.Minute ||
		cfg.BufferSize != 262144 || cfg.MaxRescans != 6 || cfg.Settle != 5*time.Minute || cfg.RescanInterval != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.Exclude) != 0 || cfg.StorageID != nil {
		t.Fatalf("exclude and storage must default to empty: %+v", cfg)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"valid drive path", func(*Config) {}, ""},
		{"valid UNC path", func(c *Config) { c.Paths = []string{`\\lab\share\runs`} }, ""},
		{"valid two roots", func(c *Config) { c.Paths = []string{`D:\a`, `E:\b`} }, ""},
		{"no paths", func(c *Config) { c.Paths = nil }, "paths must hold at least one root"},
		{"relative path", func(c *Config) { c.Paths = []string{`Projects`} }, "must be absolute"},
		{"drive without separator", func(c *Config) { c.Paths = []string{`D:Projects`} }, "must be absolute"},
		{"glob star", func(c *Config) { c.Paths = []string{`D:\CDS*`} }, "glob character"},
		{"glob question", func(c *Config) { c.Paths = []string{`D:\CDS?`} }, "glob character"},
		{"glob bracket", func(c *Config) { c.Paths = []string{`D:\CDS[1]`} }, "glob character"},
		{"duplicate roots", func(c *Config) { c.Paths = []string{`D:\a`, `d:\A\`} }, "duplicate"},
		{"nested root", func(c *Config) { c.Paths = []string{`D:\a`, `D:\a\b`} }, "nested"},
		{"sibling with shared prefix is not nested", func(c *Config) { c.Paths = []string{`D:\a`, `D:\ab`} }, ""},
		{"settle timeout below settle", func(c *Config) { c.Settle = 5 * time.Second; c.SettleTimeout = time.Second }, "settle_timeout"},
		{"negative settle", func(c *Config) { c.Settle = -time.Second }, "settle must not be negative"},
		{"start_at unknown", func(c *Config) { c.StartAt = "middle" }, "start_at"},
		{"start_at beginning", func(c *Config) { c.StartAt = "beginning" }, ""},
		{"buffer below 4096", func(c *Config) { c.BufferSize = 4095 }, "buffer_size"},
		{"buffer above 1 MiB", func(c *Config) { c.BufferSize = 1<<20 + 1 }, "buffer_size"},
		{"buffer at 4096", func(c *Config) { c.BufferSize = 4096 }, ""},
		{"negative max_rescans", func(c *Config) { c.MaxRescans = -1 }, "max_rescans"},
		{"negative max_catchup", func(c *Config) { c.MaxCatchup = -time.Hour }, "max_catchup"},
		{"negative rescan_interval", func(c *Config) { c.RescanInterval = -time.Second }, "rescan_interval"},
		{"bad exclude glob", func(c *Config) { c.Exclude = []string{`**/[`} }, "exclude"},
		{"good exclude globs", func(c *Config) { c.Exclude = []string{`**/~*`, `**/*.tmp`} }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}
