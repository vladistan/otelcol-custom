// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"defaults", func(*Config) {}, ""},
		{"one pool filter", func(c *Config) { c.Pools = []string{"tank"} }, ""},
		{"source sysctl", func(c *Config) { c.Source = "sysctl" }, ""},
		{"source unset", func(c *Config) { c.Source = "" }, ""},
		{"unknown source", func(c *Config) { c.Source = "kvm" }, `source must be auto, proc or sysctl, got "kvm"`},
		{"datasets on", func(c *Config) { c.Datasets = true }, ""},
		{"zero interval", func(c *Config) { c.CollectionInterval = 0 }, "collection_interval must be positive"},
		{"interval under a second", func(c *Config) { c.CollectionInterval = 500 * time.Millisecond }, "at least 1 second"},
		{"empty kstat_path with the proc source", func(c *Config) { c.Source = "proc"; c.KstatPath = "" }, "kstat_path must be set"},
		{"empty kstat_path with the sysctl source", func(c *Config) { c.Source = "sysctl"; c.KstatPath = "" }, ""},
		{"max_datasets zero means no cap", func(c *Config) { c.MaxDatasets = 0 }, ""},
		{"negative max_datasets", func(c *Config) { c.MaxDatasets = -1 }, "max_datasets must not be negative"},
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

func TestConfigValidateRejectsABadDatasetGlob(t *testing.T) {
	tests := map[string]func(*Config){
		"dataset_include": func(c *Config) { c.DatasetInclude = []string{"tank/[a"} },
		"dataset_exclude": func(c *Config) { c.DatasetExclude = []string{"tank/[a"} },
	}
	for key, mutate := range tests {
		bad := createDefaultConfig().(*Config)
		mutate(bad)

		err := bad.Validate()

		require.Error(t, err, key)
		assert.Contains(t, err.Error(), key)
	}
}

func TestConfigValidateAcceptsGoodDatasetGlobs(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.DatasetInclude = []string{"tank/*", "vault/data"}
	cfg.DatasetExclude = []string{"tank/tmp"}

	require.NoError(t, cfg.Validate())
}
