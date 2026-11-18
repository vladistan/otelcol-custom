// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
)

func validTarget() TargetConfig {
	return TargetConfig{Name: "db-1", Address: "https://192.0.2.5", Username: "ro", Password: "secret"} // pragma: allowlist secret
}

func validConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Targets = []TargetConfig{validTarget()}
	return cfg
}

func TestConfigLoadsAllNineTargetKeysAndTheDefaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "30s",
		"targets": []any{map[string]any{
			"name": "db-1", "address": "https://192.0.2.5", "username": "ro", "password": "secret", // pragma: allowlist secret
			"ca_file": "/etc/otelcol/idrac-ca.pem", "insecure_skip_verify": true, "resources": []any{"system", "thermal"},
			"request_timeout": "5s", "scrape_timeout": "15s",
		}, map[string]any{"name": "db-2", "address": "https://192.0.2.6", "username": "ro", "password": "secret"}}, // pragma: allowlist secret
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 30*time.Second, cfg.CollectionInterval)
	full := cfg.Targets[0].resolved()
	assert.Equal(t, "/etc/otelcol/idrac-ca.pem", full.CAFile)
	assert.True(t, full.InsecureSkipVerify)
	assert.Equal(t, []string{"system", "thermal"}, full.Resources)
	assert.Equal(t, 5*time.Second, full.RequestTimeout)
	assert.Equal(t, 15*time.Second, full.ScrapeTimeout)

	def := cfg.Targets[1].resolved()
	assert.False(t, def.InsecureSkipVerify)
	assert.Equal(t, 10*time.Second, def.RequestTimeout)
	assert.Equal(t, 30*time.Second, def.ScrapeTimeout)
	assert.ElementsMatch(t, []string{"system", "thermal", "power", "storage", "memory", "processors", "bmc"}, def.Resources)
	assert.Equal(t, time.Minute, createDefaultConfig().(*Config).CollectionInterval)
}

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"valid":                   {func(*Config) {}, ""},
		"no targets":              {func(c *Config) { c.Targets = nil }, "targets must not be empty"},
		"interval under a second": {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"zero interval":           {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"no name":                 {func(c *Config) { c.Targets[0].Name = "" }, "name must be set"},
		"duplicate name":          {func(c *Config) { c.Targets = append(c.Targets, c.Targets[0]) }, "duplicate target name"},
		"no address":              {func(c *Config) { c.Targets[0].Address = "" }, "address must be set"},
		"plain http":              {func(c *Config) { c.Targets[0].Address = "http://192.0.2.5" }, "address must be an https URL"},
		"no scheme":               {func(c *Config) { c.Targets[0].Address = "192.0.2.5" }, "address must be an https URL"},
		"no username":             {func(c *Config) { c.Targets[0].Username = "" }, "username must be set"},
		"no password":             {func(c *Config) { c.Targets[0].Password = "" }, "password must be set"},
		"unknown resource":        {func(c *Config) { c.Targets[0].Resources = []string{"gpu"} }, `unknown resource "gpu"`},
		"negative timeout":        {func(c *Config) { c.Targets[0].RequestTimeout = -time.Second }, "request_timeout must not be negative"},
		"scrape under request":    {func(c *Config) { c.Targets[0].RequestTimeout = time.Minute }, "scrape_timeout must not be below request_timeout"},
		"storage below base":      {func(c *Config) { c.Targets[0].StorageInterval = 30 * time.Second }, "storage_interval must not be below collection_interval"},
		"storage equal to base":   {func(c *Config) { c.Targets[0].StorageInterval = time.Minute }, ""},
		"storage above base":      {func(c *Config) { c.Targets[0].StorageInterval = time.Hour }, ""},
		"negative storage":        {func(c *Config) { c.Targets[0].StorageInterval = -time.Second }, "storage_interval must not be negative"},
		"negative drive cap":      {func(c *Config) { c.Targets[0].StorageMaxDrives = -1 }, "storage_max_drives must not be negative"},
		"long base, default":      {func(c *Config) { c.CollectionInterval = time.Hour }, ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			err := cfg.Validate()

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPasswordIsMaskedInAConfigDump(t *testing.T) {
	cm := confmap.New()
	require.NoError(t, cm.Marshal(validConfig()))

	assert.NotContains(t, fmt.Sprintf("%v", cm.ToStringMap()), "secret")
}

func TestConfigRejectsAnUnknownKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"targets": []any{map[string]any{"name": "x", "tls_insecure": true}}})

	err := cm.Unmarshal(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls_insecure")
}

func TestStorageKeysLoadAndDefault(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"targets": []any{map[string]any{
			"name": "db-1", "address": "https://192.0.2.5", "username": "ro", "password": "secret", // pragma: allowlist secret
			"storage_interval": "2m", "storage_max_drives": 24,
		}, map[string]any{"name": "db-2", "address": "https://192.0.2.6", "username": "ro", "password": "secret"}}, // pragma: allowlist secret
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	assert.Equal(t, 2*time.Minute, cfg.Targets[0].StorageInterval)
	assert.Equal(t, 24, cfg.Targets[0].resolved().StorageMaxDrives)
	assert.Equal(t, 256, cfg.Targets[1].resolved().StorageMaxDrives)
	assert.Equal(t, 5*time.Minute, cfg.Targets[1].storageInterval(cfg.CollectionInterval))
}
