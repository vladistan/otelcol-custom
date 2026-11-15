// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"slices"
	"strings"
	"testing"
)

func defaultConfig() *Config {
	return NewFactory().CreateDefaultConfig().(*Config)
}

func TestDefaultConfig_ValidatesWithoutAnySetting(t *testing.T) {
	if err := defaultConfig().Validate(); err != nil {
		t.Fatalf("default config does not validate: %v", err)
	}
}

func TestDefaultConfig_Values(t *testing.T) {
	cfg := defaultConfig()

	if cfg.TimeZone != "Local" {
		t.Errorf("time_zone = %q, want Local", cfg.TimeZone)
	}
	want := []string{
		"Control client", "Recieved control message", "process memory usage",
		"new client, process memory usage", "Shared flag", "Initial view-only state",
		"RFB initialization phase 1 completed", "Requested to start up extra RFB servers",
		"Requested to shut down extra RFB servers", "Considering to reload extra RFB servers",
		"Same Extra Ports configuration", "Timeout on acquire frame", "DesktopSrvDispatcher",
		"View port", "Constrained", "Active console session Id", "Destination dirty rect",
		"update detected", "Acquire frame", "get frame buffer", "Time between",
		"UpdateFilter", "WinVideoRegionUpdaterImpl",
	}
	if !slices.Equal(cfg.DropPrefixes, want) {
		t.Errorf("drop_prefixes = %q, want %q", cfg.DropPrefixes, want)
	}
}

func TestValidate_RejectsANamedTimeZoneAndSaysOnlyLocalAndUTCAreSupported(t *testing.T) {
	cfg := defaultConfig()
	cfg.TimeZone = "America/New_York"

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "only Local and UTC are supported") {
		t.Errorf("Validate() = %v, want an error saying only Local and UTC are supported", err)
	}
}

func TestValidate_RejectsAnUnknownTimeZone(t *testing.T) {
	cfg := defaultConfig()
	cfg.TimeZone = "Mars/Olympus_Mons"

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "time_zone") {
		t.Errorf("Validate() = %v, want an error naming time_zone", err)
	}
}

func TestValidate_RejectsAnEmptyTimeZoneAndSaysOnlyLocalAndUTCAreSupported(t *testing.T) {
	cfg := defaultConfig()
	cfg.TimeZone = ""

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "only Local and UTC are supported") {
		t.Errorf("Validate() = %v, want an error saying only Local and UTC are supported", err)
	}
}

func TestValidate_RejectsAnEmptyDropPrefix(t *testing.T) {
	cfg := defaultConfig()
	cfg.DropPrefixes = []string{"Control client", ""}

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "drop_prefixes") {
		t.Errorf("Validate() = %v, want an error naming drop_prefixes", err)
	}
}

func TestValidate_AcceptsAnEmptyDropPrefixList(t *testing.T) {
	cfg := defaultConfig()
	cfg.DropPrefixes = nil

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil: an empty list drops nothing", err)
	}
}

func TestValidate_AcceptsLocalAndUTC(t *testing.T) {
	for _, zone := range []string{"Local", "UTC"} {
		cfg := defaultConfig()
		cfg.TimeZone = zone

		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with time_zone %q = %v, want nil", zone, err)
		}
	}
}
