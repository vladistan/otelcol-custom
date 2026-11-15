// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"defaults", func(*Config) {}, ""},
		{"one pool filter", func(c *Config) { c.Pools = []string{"tank"} }, ""},
		{"zero interval", func(c *Config) { c.CollectionInterval = 0 }, "collection_interval must be positive"},
		{"interval under a second", func(c *Config) { c.CollectionInterval = 500 * time.Millisecond }, "at least 1 second"},
		{"empty kstat_path", func(c *Config) { c.KstatPath = "" }, "kstat_path must be set"},
		{"empty pool name", func(c *Config) { c.Pools = []string{"tank", ""} }, "pools entry 1 is empty"},
		{"empty excluded pool name", func(c *Config) { c.ExcludePools = []string{""} }, "exclude_pools entry 0 is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			tt.mutate(cfg)

			err := cfg.Validate()

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
