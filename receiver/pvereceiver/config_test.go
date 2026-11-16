// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/confmap"
)

func validConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.BaseURLs = []string{"https://pve1.example:8006"}
	cfg.Resources.Enabled = true
	cfg.Resources.TokenID = "otel@pve!resources"
	cfg.Resources.TokenSecret = "resources-secret" // pragma: allowlist secret
	return cfg
}

func TestDefaultsAreTheDocumentedOnes(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.False(t, cfg.InsecureSkipVerify, "verification is on by default")
	assert.False(t, cfg.Resources.Enabled)
	assert.False(t, cfg.Guests.Enabled)
	assert.Equal(t, 60*time.Second, cfg.Resources.CollectionInterval)
	assert.Equal(t, 10*time.Second, cfg.Resources.Timeout)
	assert.Equal(t, 15*time.Minute, cfg.Guests.CollectionInterval)
	assert.Equal(t, 5*time.Second, cfg.Guests.Timeout)
}

func TestValidConfigPasses(t *testing.T) {
	assert.NoError(t, validConfig().Validate())
}

func TestConfigNeedsAnEnabledScraper(t *testing.T) {
	cfg := validConfig()
	cfg.Resources.Enabled = false

	assert.ErrorContains(t, cfg.Validate(), "resources or guests")
}

func TestEnabledScraperWithAnEmptyTokenKeyIsRejected(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"resources token_id":     func(c *Config) { c.Resources.TokenID = "" },
		"resources token_secret": func(c *Config) { c.Resources.TokenSecret = "" },
		"guests token_id": func(c *Config) {
			c.Guests.Enabled, c.Guests.TokenSecret = true, "s" // pragma: allowlist secret
		},
		"guests token_secret": func(c *Config) {
			c.Guests.Enabled, c.Guests.TokenID = true, "otel@pve!guests"
		},
	} {
		cfg := validConfig()
		mutate(cfg)

		assert.Error(t, cfg.Validate(), name)
	}
}

func TestDisabledScraperNeedsNoToken(t *testing.T) {
	cfg := validConfig()
	cfg.Guests.Enabled = false
	cfg.Guests.TokenID = ""

	assert.NoError(t, cfg.Validate())
}

func TestBaseURLsAreChecked(t *testing.T) {
	for _, urls := range [][]string{nil, {"://x"}, {"ftp://host"}, {"https://"}, {"host:8006"}, {""}} {
		cfg := validConfig()
		cfg.BaseURLs = urls

		assert.Error(t, cfg.Validate(), fmt.Sprint(urls))
	}
}

func TestIntervalAndTimeoutMustBePositive(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"interval": func(c *Config) { c.Resources.CollectionInterval = 0 },
		"timeout":  func(c *Config) { c.Resources.Timeout = -time.Second },
	} {
		cfg := validConfig()
		mutate(cfg)

		assert.Error(t, cfg.Validate(), name)
	}
}

func TestEachScraperHasItsOwnToken(t *testing.T) {
	cfg := validConfig()
	cfg.Guests.Enabled = true
	cfg.Guests.TokenID = "otel@pve!guests"
	cfg.Guests.TokenSecret = "guests-secret" // pragma: allowlist secret

	require.NoError(t, cfg.Validate())
	assert.NotEqual(t, cfg.Resources.TokenID, cfg.Guests.TokenID)
	assert.NotEqual(t, cfg.Resources.TokenSecret, cfg.Guests.TokenSecret)
}

func TestConfigLoadsFromTheConfigurationKeys(t *testing.T) {
	conf := confmap.NewFromStringMap(map[string]any{
		"base_urls":            []any{"https://a:8006", "https://b:8006"},
		"ca_file":              "/etc/pve-ca.pem",
		"insecure_skip_verify": false,
		"resources": map[string]any{
			"enabled": true, "token_id": "otel@pve!r", "token_secret": "rs",
			"collection_interval": "30s", "timeout": "8s", "subscription": true,
		},
		"guests": map[string]any{
			"enabled": true, "token_id": "otel@pve!g", "token_secret": "gs", "node": "pve12",
		},
	})
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, conf.Unmarshal(cfg))

	assert.Equal(t, []string{"https://a:8006", "https://b:8006"}, cfg.BaseURLs)
	assert.Equal(t, "/etc/pve-ca.pem", cfg.CAFile)
	assert.Equal(t, 30*time.Second, cfg.Resources.CollectionInterval)
	assert.True(t, cfg.Resources.Subscription)
	assert.Equal(t, "pve12", cfg.Guests.Node)
	assert.Equal(t, 15*time.Minute, cfg.Guests.CollectionInterval, "an unset key keeps its default")
	assert.NoError(t, cfg.Validate())
}

func TestTokenSecretIsMaskedWhenTheConfigIsMarshalled(t *testing.T) {
	cfg := validConfig()
	out := confmap.New()
	require.NoError(t, out.Marshal(cfg))

	assert.NotContains(t, fmt.Sprint(out.ToStringMap()), "resources-secret")
	assert.Equal(t, configopaque.String("resources-secret"), cfg.Resources.TokenSecret)
}
