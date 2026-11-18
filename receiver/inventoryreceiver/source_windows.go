// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package inventoryreceiver

func newSource(cfg *Config) (source, error) {
	return &windowsSource{api: systemAPI{}, maxDrives: cfg.MaxDrives}, nil
}
