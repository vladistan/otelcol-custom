// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Router is the site data of one flow exporter, keyed by its address.
type Router struct {
	// Address is the IP address the router exports from.
	Address string `mapstructure:"address"`
	// AccountID is the site name written to account_id.
	AccountID string `mapstructure:"account_id"`
	// Region is the site name written to region.
	Region string `mapstructure:"region"`
	// SamplingRate is used when a record carries none; 0 means 1.
	SamplingRate int32 `mapstructure:"sampling_rate"`
	// Direction is written to flow_direction when a record carries none.
	Direction string `mapstructure:"direction"`
}

// Config is the exporter configuration.
type Config struct {
	TimeoutSettings exporterhelper.TimeoutConfig                             `mapstructure:",squash"`
	BackOffConfig   configretry.BackOffConfig                                `mapstructure:"retry_on_failure"`
	QueueSettings   configoptional.Optional[exporterhelper.QueueBatchConfig] `mapstructure:"sending_queue"`

	// Endpoint is host:port of the ClickHouse native protocol.
	Endpoint string `mapstructure:"endpoint"`
	// Database and Table name the flow table.
	Database string `mapstructure:"database"`
	Table    string `mapstructure:"table"`
	// Username and Password are the insert-only account; the password comes from an environment variable.
	Username string              `mapstructure:"username"`
	Password configopaque.String `mapstructure:"password"`
	// PasswordVariable is the name of the environment variable the password setting reads. It is used
	// only to name the variable in the warning for an empty password, and holds a name, never a value.
	PasswordVariable string `mapstructure:"password_variable"`
	// Loader is written to the loader column.
	Loader string `mapstructure:"loader"`
	// Collector is written to the collector column; empty means the host name of the process.
	Collector string `mapstructure:"collector"`
	// Routers is the lookup of site data by exporter address.
	Routers []Router `mapstructure:"routers"`
}

func createDefaultConfig() component.Config {
	queue := exporterhelper.NewDefaultQueueConfig()
	queue.QueueSize = 1000
	return &Config{
		TimeoutSettings: exporterhelper.NewDefaultTimeoutConfig(),
		BackOffConfig:   configretry.NewDefaultBackOffConfig(),
		QueueSettings:   configoptional.Some(queue),
		Database:        "flowlogs",
		Table:           "flow_log",
		Loader:          "flowlogexporter",
	}
}

// Validate checks the endpoint, the table names, the routers and the queue.
func (c *Config) Validate() error {
	var errs []error
	if c.Endpoint == "" {
		errs = append(errs, errors.New("endpoint must be set"))
	}
	if !identifier.MatchString(c.Database) {
		errs = append(errs, fmt.Errorf("database %q is not a plain identifier", c.Database))
	}
	if !identifier.MatchString(c.Table) {
		errs = append(errs, fmt.Errorf("table %q is not a plain identifier", c.Table))
	}
	if v := c.PasswordVariable; v != "" {
		if !identifier.MatchString(v) {
			errs = append(errs, errors.New("password_variable must be the name of an environment variable"))
		} else if v == string(c.Password) {
			errs = append(errs, errors.New("password_variable must be a variable name, not the password"))
		}
	}
	seen := map[netip.Addr]bool{}
	for i, r := range c.Routers {
		addr, err := netip.ParseAddr(r.Address)
		if err != nil {
			errs = append(errs, fmt.Errorf("routers[%d]: invalid address %q", i, r.Address))
			continue
		}
		addr = addr.Unmap()
		if seen[addr] {
			errs = append(errs, fmt.Errorf("routers[%d]: duplicate address %q", i, r.Address))
		}
		seen[addr] = true
		if r.SamplingRate < 0 {
			errs = append(errs, fmt.Errorf("routers[%d]: sampling_rate must not be negative", i))
		}
	}
	if q := c.QueueSettings.Get(); q != nil && q.QueueSize <= 0 {
		errs = append(errs, errors.New("sending_queue.queue_size must be positive"))
	}
	return errors.Join(errs...)
}
