// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig() *Config {
	return &Config{
		CollectionInterval: time.Minute,
		Timeout:            10 * time.Second,
		Printers: []Printer{
			{Name: "lab-a", Endpoint: "ipp://printer-a.example:631/ipp/print"},
			{Name: "lab-b", Endpoint: "ipps://printer-b.example/ipp/print"},
		},
	}
}

func TestValidateAcceptsIPPAndIPPS(t *testing.T) {
	require.NoError(t, validConfig().Validate())
}

func TestValidateNamesTheFailingEntry(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"empty list", func(c *Config) { c.Printers = nil }, "printers must not be empty"},
		{"duplicate name", func(c *Config) { c.Printers[1].Name = "lab-a" }, `printers[1]: duplicate name "lab-a"`},
		{"empty name", func(c *Config) { c.Printers[0].Name = "" }, "printers[0]: name is empty"},
		{"http endpoint", func(c *Config) { c.Printers[0].Endpoint = "http://printer-a.example/ipp/print" },
			`printers[0] "lab-a": endpoint must be an ipp:// or ipps:// URI`},
		{"no scheme", func(c *Config) { c.Printers[1].Endpoint = "printer-b.example" },
			`printers[1] "lab-b": endpoint must be an ipp:// or ipps:// URI`},
		{"no host", func(c *Config) { c.Printers[0].Endpoint = "ipp:///ipp/print" },
			`printers[0] "lab-a": endpoint has no host`},
		{"long endpoint", func(c *Config) { c.Printers[0].Endpoint = "ipp://printer.example/" + strings.Repeat("a", 1100) },
			`printers[0] "lab-a": endpoint is longer than 1024 bytes`},
		{"zero interval", func(c *Config) { c.CollectionInterval = 0 }, "collection_interval must be positive"},
		{"negative interval", func(c *Config) { c.CollectionInterval = -time.Second }, "collection_interval must be positive"},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, "timeout must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.change(cfg)

			err := cfg.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
