// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
)

func validConfig() *Config {
	return &Config{
		MaxCatchup: defaultMaxCatchup,
		Accounts: []AccountConfig{
			{Name: "prod", RoleARN: "arn:aws:iam::111111111111:role/ro", ExternalID: "ext", Regions: []string{"us-east-1", "eu-west-1"}},
		},
		Tiers: map[string]TierConfig{
			"operational": {
				CollectionInterval: 15 * time.Minute, Period: 15 * time.Minute, Delay: 10 * time.Minute,
				Stats: []string{"Average", "Maximum"}, Limit: 100,
				Namespaces: map[string][]string{"AWS/EC2": {"CPUUtilization"}, "AWS/RDS": nil},
			},
		},
	}
}

func TestConfigValidateAcceptsAFullConfig(t *testing.T) {
	assert.NoError(t, validConfig().Validate())
}

func TestConfigValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"no accounts", func(c *Config) { c.Accounts = nil }, "accounts must not be empty"},
		{"unnamed account", func(c *Config) { c.Accounts[0].Name = "" }, "name must be set"},
		{"duplicate account", func(c *Config) { c.Accounts = append(c.Accounts, c.Accounts[0]) }, "duplicate account"},
		{"no regions", func(c *Config) { c.Accounts[0].Regions = nil }, "regions must not be empty"},
		{"empty region", func(c *Config) { c.Accounts[0].Regions = []string{""} }, "region 0 is empty"},
		{"external id without role", func(c *Config) { c.Accounts[0].RoleARN = "" }, "external_id needs role_arn"},
		{"no tiers", func(c *Config) { c.Tiers = nil }, "tiers must not be empty"},
		{"negative catch-up", func(c *Config) { c.MaxCatchup = -time.Hour }, "max_catchup must not be negative"},
		{"empty stats", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.Stats = nil }) }, "stats must not be empty"},
		{"empty stat name", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.Stats = []string{"Average", ""} })
		}, "stat 1 is empty"},
		{"interval below period", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = 5 * time.Minute; t.Period = 15 * time.Minute })
		}, "collection_interval must not be below period"},
		{"interval not a multiple of period", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = 20 * time.Minute; t.Period = 15 * time.Minute })
		}, "collection_interval must be a multiple of period"},
		{"duplicate region", func(c *Config) { c.Accounts[0].Regions = []string{"us-east-1", "us-east-1"} }, "duplicate region"},
		{"duplicate stat", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.Stats = []string{"Average", "Maximum", "Average"} })
		}, "duplicate stat"},
		{"duplicate metric name", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) {
				t.Namespaces = map[string][]string{"AWS/EC2": {"CPUUtilization", "CPUUtilization"}}
			})
		}, "duplicate metric name"},
		{"sub-second period", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = time.Second; t.Period = 500 * time.Millisecond })
		}, "period must be a whole number of seconds"},
		{"fractional second period", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = time.Minute; t.Period = 1500 * time.Millisecond })
		}, "period must be a whole number of seconds"},
		{"period of 45 seconds", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = 90 * time.Second; t.Period = 45 * time.Second })
		}, "period must be 1, 5, 10 or 30 seconds, or a multiple of 60 seconds"},
		{"period of 90 seconds", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = 90 * time.Second; t.Period = 90 * time.Second })
		}, "period must be 1, 5, 10 or 30 seconds, or a multiple of 60 seconds"},
		{"zero period", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.Period = 0 }) }, "period must be positive"},
		{"zero interval", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.CollectionInterval = 0 }) }, "collection_interval must be positive"},
		{"negative delay", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.Delay = -time.Second }) }, "delay must not be negative"},
		{"zero limit", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.Limit = 0 }) }, "limit must be positive"},
		{"no namespaces", func(c *Config) { c.mutateTier("operational", func(t *TierConfig) { t.Namespaces = nil }) }, "namespaces must not be empty"},
		{"empty namespace", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.Namespaces = map[string][]string{"": {"X"}} })
		}, "namespace name is empty"},
		{"empty metric name", func(c *Config) {
			c.mutateTier("operational", func(t *TierConfig) { t.Namespaces = map[string][]string{"AWS/EC2": {""}} })
		}, "metric name 0 is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			assert.ErrorContains(t, cfg.Validate(), tt.wantErr)
		})
	}
}

func TestConfigAcceptsTheCloudWatchPeriods(t *testing.T) {
	for _, period := range []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 24 * time.Hour} {
		t.Run(period.String(), func(t *testing.T) {
			cfg := validConfig()
			cfg.mutateTier("operational", func(tier *TierConfig) { tier.CollectionInterval = 2 * period; tier.Period = period })

			assert.NoError(t, cfg.Validate())
		})
	}
}

func TestConfigAcceptsTheSameRegionInTwoAccounts(t *testing.T) {
	cfg := validConfig()
	second := cfg.Accounts[0]
	second.Name = "other"
	cfg.Accounts = append(cfg.Accounts, second)

	assert.NoError(t, cfg.Validate())
}

func (c *Config) mutateTier(name string, fn func(*TierConfig)) {
	tier := c.Tiers[name]
	fn(&tier)
	c.Tiers[name] = tier
}

func TestConfigLoadsATierWithAllFiveKeys(t *testing.T) {
	conf := confmap.NewFromStringMap(map[string]any{
		"accounts": []any{map[string]any{"name": "prod", "regions": []any{"us-east-1"}}},
		"tiers": map[string]any{"daily": map[string]any{
			"collection_interval": "24h", "period": "24h", "delay": "1h",
			"stats": []any{"Average", "p50"}, "limit": 50,
			"namespaces": map[string]any{"AWS/EC2": []any{}},
		}},
	})
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, conf.Unmarshal(cfg))

	tier := cfg.Tiers["daily"]
	assert.Equal(t, 24*time.Hour, tier.CollectionInterval)
	assert.Equal(t, 24*time.Hour, tier.Period)
	assert.Equal(t, time.Hour, tier.Delay)
	assert.Equal(t, []string{"Average", "p50"}, tier.Stats)
	assert.Equal(t, 50, tier.Limit)
	assert.NoError(t, cfg.Validate())
}

func TestConfigRejectsStoredCredentialKeys(t *testing.T) {
	for _, key := range []string{"access_key_id", "secret_access_key", "session_token"} {
		t.Run(key, func(t *testing.T) {
			conf := confmap.NewFromStringMap(map[string]any{
				"accounts": []any{map[string]any{"name": "prod", "regions": []any{"us-east-1"}, key: "x"}},
			})

			assert.Error(t, conf.Unmarshal(createDefaultConfig()), "a stored credential key must not be accepted")
		})
	}
}

func TestDefaultConfigStructureAndCatchup(t *testing.T) {
	require.NoError(t, componenttest.CheckConfigStruct(createDefaultConfig()))
	assert.Equal(t, 72*time.Hour, createDefaultConfig().(*Config).MaxCatchup)
}
