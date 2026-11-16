// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
)

// Config lists the BMCs to poll and the limits that keep one slow board from stalling the others.
type Config struct {
	// CollectionInterval is the time between two scrapes of each BMC.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// MaxConcurrent is the most BMCs read at once.
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// ExchangeTimeout limits one request and its answer; one retry follows a timeout.
	ExchangeTimeout time.Duration `mapstructure:"exchange_timeout"`
	// ScrapeTimeout limits the whole read of one BMC.
	ScrapeTimeout time.Duration `mapstructure:"scrape_timeout"`
	BMCs          []BMCConfig   `mapstructure:"bmcs"`
}

// BMCConfig is one board.
type BMCConfig struct {
	Name     string              `mapstructure:"name"`
	Address  string              `mapstructure:"address"` // host or host:port, port 623 by default
	Username string              `mapstructure:"username"`
	Password configopaque.String `mapstructure:"password"`
	Vendor   string              `mapstructure:"vendor"` // dell or supermicro
	// CipherSuite is required for IPMI 2.0; the receiver never probes suites, because
	// every probe is one more failed login on a shared account.
	CipherSuite *int `mapstructure:"cipher_suite"`
	// IPMIVersion is 2.0 (default), 1.5 or auto.
	IPMIVersion string `mapstructure:"ipmi_version"`
	// PrivilegeLevel of the sensor session: user (default), operator or administrator.
	PrivilegeLevel string `mapstructure:"privilege_level"`
	// DCMIPower reads the host power over a separate administrator session.
	DCMIPower     bool          `mapstructure:"dcmi_power"`
	PowerInterval time.Duration `mapstructure:"power_interval"` // 5m by default
}

var _ component.Config = (*Config)(nil)

const (
	defaultIPMIVersion   = "2.0"
	defaultPrivilege     = "user"
	defaultPowerInterval = 5 * time.Minute
	minPowerInterval     = time.Minute
)

// resolved returns a copy of the BMC with the defaults filled in.
func (b BMCConfig) resolved() BMCConfig {
	if b.IPMIVersion == "" {
		b.IPMIVersion = defaultIPMIVersion
	}
	if b.PrivilegeLevel == "" {
		b.PrivilegeLevel = defaultPrivilege
	}
	if b.PowerInterval == 0 {
		b.PowerInterval = defaultPowerInterval
	}
	return b
}

// Validate checks the limits and every BMC.
func (c *Config) Validate() error {
	switch {
	case c.CollectionInterval <= 0:
		return errors.New("collection_interval must be positive")
	case c.CollectionInterval < time.Second:
		return errors.New("collection_interval must be at least 1 second")
	case c.MaxConcurrent < 1:
		return errors.New("max_concurrent must be at least 1")
	case c.ExchangeTimeout <= 0:
		return errors.New("exchange_timeout must be positive")
	case c.ScrapeTimeout < c.ExchangeTimeout:
		return errors.New("scrape_timeout must not be below exchange_timeout")
	case len(c.BMCs) == 0:
		return errors.New("bmcs must not be empty")
	}
	seen := map[string]bool{}
	for i, b := range c.BMCs {
		if err := b.resolved().validate(i, seen); err != nil {
			return err
		}
	}
	return nil
}

func (b BMCConfig) validate(index int, seen map[string]bool) error {
	switch {
	case b.Name == "":
		return fmt.Errorf("bmcs[%d]: name must be set", index)
	case seen[b.Name]:
		return fmt.Errorf("duplicate bmc name %q", b.Name)
	}
	seen[b.Name] = true
	switch {
	case b.Address == "":
		return fmt.Errorf("bmc %q: address must be set", b.Name)
	case b.Username == "":
		return fmt.Errorf("bmc %q: username must be set", b.Name)
	case b.Password == "":
		return fmt.Errorf("bmc %q: password must be set", b.Name)
	case b.Vendor != "dell" && b.Vendor != "supermicro":
		return fmt.Errorf("bmc %q: vendor must be dell or supermicro", b.Name)
	case b.IPMIVersion != "2.0" && b.IPMIVersion != "1.5" && b.IPMIVersion != "auto":
		return fmt.Errorf("bmc %q: ipmi_version must be 2.0, 1.5 or auto", b.Name)
	case b.PrivilegeLevel != "user" && b.PrivilegeLevel != "operator" && b.PrivilegeLevel != "administrator":
		return fmt.Errorf("bmc %q: privilege_level must be user, operator or administrator", b.Name)
	case b.PowerInterval < 0:
		return fmt.Errorf("bmc %q: power_interval must not be negative", b.Name)
	case b.PowerInterval < minPowerInterval:
		return fmt.Errorf("bmc %q: power_interval must be at least %s: every read opens an administrator session", b.Name, minPowerInterval)
	}
	if b.CipherSuite != nil && (*b.CipherSuite < 0 || *b.CipherSuite > 19) {
		return fmt.Errorf("bmc %q: cipher_suite must be between 0 and 19", b.Name)
	}
	if b.IPMIVersion != "1.5" && b.CipherSuite == nil {
		return fmt.Errorf("bmc %q: cipher_suite is required for IPMI 2.0", b.Name)
	}
	return nil
}
