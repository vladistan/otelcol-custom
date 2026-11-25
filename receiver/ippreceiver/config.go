// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config lists the printers to poll and how often.
type Config struct {
	// CollectionInterval is the time between two polls of one printer.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	// Timeout bounds one request, from connect to the last byte of the answer.
	Timeout time.Duration `mapstructure:"timeout"`
	// Printers is the static list of printers.
	Printers []Printer `mapstructure:"printers"`
}

// Printer is one entry of the printer list.
type Printer struct {
	// Name is the unique label of the printer; it becomes hw.id and hw.name.
	Name string `mapstructure:"name"`
	// Endpoint is an ipp:// or ipps:// URI, for example ipp://host:631/ipp/print.
	Endpoint string `mapstructure:"endpoint"`
}

// maxEndpointBytes keeps the printer-uri attribute below the 16-bit IPP length field.
const maxEndpointBytes = 1024

var _ component.Config = (*Config)(nil)

// Validate rejects a non-positive interval or timeout, an empty list, an empty or
// duplicate name, and an endpoint that is not an ipp:// or ipps:// URI with a host.
func (cfg *Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return errors.New("collection_interval must be positive")
	}
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if len(cfg.Printers) == 0 {
		return errors.New("printers must not be empty")
	}
	seen := make(map[string]struct{}, len(cfg.Printers))
	for i, p := range cfg.Printers {
		if p.Name == "" {
			return fmt.Errorf("printers[%d]: name is empty", i)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("printers[%d]: duplicate name %q", i, p.Name)
		}
		seen[p.Name] = struct{}{}
		if err := validEndpoint(p.Endpoint); err != nil {
			return fmt.Errorf("printers[%d] %q: %w", i, p.Name, err)
		}
	}
	return nil
}

func validEndpoint(endpoint string) error {
	if len(endpoint) > maxEndpointBytes {
		return fmt.Errorf("endpoint is longer than %d bytes", maxEndpointBytes)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint must be an ipp:// or ipps:// URI: %w", err)
	}
	if u.Scheme != "ipp" && u.Scheme != "ipps" {
		return errors.New("endpoint must be an ipp:// or ipps:// URI")
	}
	if u.Hostname() == "" {
		return errors.New("endpoint has no host")
	}
	return nil
}
