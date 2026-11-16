// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"
)

func validBMC() BMCConfig {
	suite := 3
	return BMCConfig{Name: "pve12", Address: "192.0.2.12", Username: "ro", Password: "secret", Vendor: "supermicro", CipherSuite: &suite}
}

func validConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.BMCs = []BMCConfig{validBMC()}
	return cfg
}

func TestConfigDefaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, time.Minute, cfg.CollectionInterval)
	assert.Equal(t, 8, cfg.MaxConcurrent)
	assert.Equal(t, 3*time.Second, cfg.ExchangeTimeout)
	assert.Equal(t, 20*time.Second, cfg.ScrapeTimeout)
}

func TestConfigLoadsAllTenBMCKeysAndTheDefaults(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{
		"collection_interval": "30s",
		"bmcs": []any{map[string]any{
			"name": "pve12", "address": "192.0.2.12:623", "username": "ro", "password": "secret", "vendor": "supermicro",
			"cipher_suite": 3, "ipmi_version": "2.0", "privilege_level": "administrator", "dcmi_power": true, "power_interval": "10m",
		}, map[string]any{
			"name": "fw7", "address": "192.0.2.7", "username": "ro", "password": "secret", "vendor": "supermicro", "cipher_suite": 3,
		}},
	})

	require.NoError(t, cm.Unmarshal(cfg))
	require.NoError(t, cfg.Validate())

	full := cfg.BMCs[0].resolved()
	assert.Equal(t, "pve12", full.Name)
	assert.Equal(t, "192.0.2.12:623", full.Address)
	assert.Equal(t, "2.0", full.IPMIVersion)
	assert.Equal(t, "administrator", full.PrivilegeLevel)
	assert.True(t, full.DCMIPower)
	assert.Equal(t, 10*time.Minute, full.PowerInterval)
	assert.Equal(t, 3, *full.CipherSuite)

	def := cfg.BMCs[1].resolved()
	assert.Equal(t, "2.0", def.IPMIVersion)
	assert.Equal(t, "user", def.PrivilegeLevel)
	assert.False(t, def.DCMIPower)
	assert.Equal(t, 5*time.Minute, def.PowerInterval)
}

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"valid":                       {func(*Config) {}, ""},
		"no bmcs":                     {func(c *Config) { c.BMCs = nil }, "bmcs must not be empty"},
		"zero interval":               {func(c *Config) { c.CollectionInterval = 0 }, "collection_interval"},
		"interval under a second":     {func(c *Config) { c.CollectionInterval = time.Millisecond }, "at least 1 second"},
		"zero max concurrent":         {func(c *Config) { c.MaxConcurrent = 0 }, "max_concurrent"},
		"zero exchange timeout":       {func(c *Config) { c.ExchangeTimeout = 0 }, "exchange_timeout"},
		"scrape under exchange":       {func(c *Config) { c.ScrapeTimeout = time.Second }, "scrape_timeout must not be below exchange_timeout"},
		"no name":                     {func(c *Config) { c.BMCs[0].Name = "" }, "name must be set"},
		"duplicate name":              {func(c *Config) { c.BMCs = append(c.BMCs, c.BMCs[0]) }, "duplicate bmc name"},
		"no address":                  {func(c *Config) { c.BMCs[0].Address = "" }, "address must be set"},
		"no username":                 {func(c *Config) { c.BMCs[0].Username = "" }, "username must be set"},
		"no password":                 {func(c *Config) { c.BMCs[0].Password = "" }, "password must be set"},
		"unknown vendor":              {func(c *Config) { c.BMCs[0].Vendor = "acme" }, "vendor must be dell or supermicro"},
		"unknown ipmi version":        {func(c *Config) { c.BMCs[0].IPMIVersion = "3.0" }, "ipmi_version must be 2.0, 1.5 or auto"},
		"unknown privilege":           {func(c *Config) { c.BMCs[0].PrivilegeLevel = "root" }, "privilege_level must be user, operator or administrator"},
		"cipher suite out of range":   {func(c *Config) { s := 20; c.BMCs[0].CipherSuite = &s }, "cipher_suite must be between 0 and 19"},
		"2.0 without cipher suite":    {func(c *Config) { c.BMCs[0].CipherSuite = nil }, "cipher_suite is required for IPMI 2.0"},
		"1.5 without cipher suite ok": {func(c *Config) { c.BMCs[0].CipherSuite = nil; c.BMCs[0].IPMIVersion = "1.5" }, ""},
		"negative power interval":     {func(c *Config) { c.BMCs[0].PowerInterval = -time.Minute }, "power_interval must not be negative"},
		"power interval too short":    {func(c *Config) { c.BMCs[0].PowerInterval = time.Second }, "power_interval must be at least 1m0s"},
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

func TestAutoIPMIVersionStillNeedsACipherSuite(t *testing.T) {
	cfg := validConfig()
	cfg.BMCs[0].IPMIVersion = "auto"
	cfg.BMCs[0].CipherSuite = nil

	assert.ErrorContains(t, cfg.Validate(), "cipher_suite is required")
}

func TestConfigRejectsAnUnknownBMCKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cm := confmap.NewFromStringMap(map[string]any{"bmcs": []any{map[string]any{"name": "x", "probe_ciphers": true}}})

	err := cm.Unmarshal(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "probe_ciphers")
}

func TestPasswordIsMaskedInAConfigDump(t *testing.T) {
	cfg := validConfig()

	dump := confmapString(t, cfg)

	assert.NotContains(t, dump, "secret")
}

func confmapString(t *testing.T, cfg *Config) string {
	t.Helper()
	cm := confmap.New()
	require.NoError(t, cm.Marshal(cfg))
	return fmt.Sprintf("%v", cm.ToStringMap())
}
