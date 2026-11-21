// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
)

// ScraperConfig holds the keys both scrapers share. Each scraper has its own token.
type ScraperConfig struct {
	// Enabled switches the scraper on; both are off by default.
	Enabled bool `mapstructure:"enabled"`
	// TokenID is the full API token id, user@realm!tokenname.
	TokenID string `mapstructure:"token_id"`
	// TokenSecret is the token secret; it is masked in every output.
	TokenSecret configopaque.String `mapstructure:"token_secret"`
	// CollectionInterval is the time between two runs of the scraper.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// Timeout bounds one API request.
	Timeout time.Duration `mapstructure:"timeout"`
}

// ResourcesConfig sets the cluster resources scraper. The four extra calls are off by default.
type ResourcesConfig struct {
	ScraperConfig `mapstructure:",squash"`
	// Subscription reads /nodes/{node}/subscription of every online node.
	Subscription bool `mapstructure:"subscription"`
	// Replication reads /cluster/replication.
	Replication bool `mapstructure:"replication"`
	// BackupInfo reads /cluster/backup-info/not-backed-up.
	BackupInfo bool `mapstructure:"backup_info"`
	// GuestConfig reads the config of every guest, one call each, for onboot.
	GuestConfig bool `mapstructure:"guest_config"`
}

// GuestsConfig sets the guest agent scraper.
type GuestsConfig struct {
	ScraperConfig `mapstructure:",squash"`
	// Node is the one node whose guests are read; empty means the local host name.
	Node string `mapstructure:"node"`
}

// Config is the configuration of the pve receiver.
type Config struct {
	// BaseURLs lists the API addresses, such as https://node1:8006; the next one is tried after a failure.
	BaseURLs []string `mapstructure:"base_urls"`
	// CAFile is a PEM file with the certificate authority of the cluster.
	CAFile string `mapstructure:"ca_file"`
	// InsecureSkipVerify turns certificate checks off; it is an explicit opt-in.
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`

	Resources ResourcesConfig `mapstructure:"resources"`
	Guests    GuestsConfig    `mapstructure:"guests"`
}

var _ component.Config = (*Config)(nil)

func createDefaultConfig() component.Config {
	return &Config{
		Resources: ResourcesConfig{ScraperConfig: ScraperConfig{
			CollectionInterval: 60 * time.Second,
			Timeout:            10 * time.Second,
		}},
		Guests: GuestsConfig{ScraperConfig: ScraperConfig{
			CollectionInterval: 15 * time.Minute,
			Timeout:            5 * time.Second,
		}},
	}
}

// Validate checks the addresses and each enabled scraper at load time.
func (cfg *Config) Validate() error {
	if len(cfg.BaseURLs) == 0 {
		return errors.New("base_urls must not be empty")
	}
	for i, raw := range cfg.BaseURLs {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("base_urls[%d] is not a valid URL: %w", i, err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("base_urls[%d] must be an http or https URL with a host", i)
		}
	}
	if !cfg.Resources.Enabled && !cfg.Guests.Enabled {
		return errors.New("enable resources or guests")
	}
	if cfg.Resources.Enabled {
		if err := cfg.Resources.validate("resources"); err != nil {
			return err
		}
	}
	if cfg.Guests.Enabled {
		if err := cfg.Guests.validate("guests"); err != nil {
			return err
		}
	}
	return nil
}

func (s ScraperConfig) validate(name string) error {
	if s.TokenID == "" {
		return fmt.Errorf("%s.token_id must not be empty", name)
	}
	if s.TokenSecret == "" {
		return fmt.Errorf("%s.token_secret must not be empty", name)
	}
	if s.CollectionInterval <= 0 {
		return fmt.Errorf("%s.collection_interval must be positive", name)
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("%s.timeout must be positive", name)
	}
	return nil
}
