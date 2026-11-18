// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package inventoryreceiver

import "os"

func newSource(cfg *Config) (source, error) {
	return &linuxSource{fsys: os.DirFS("/"), maxDrives: cfg.MaxDrives}, nil
}
