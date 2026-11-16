// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package hwmonreceiver

import "go.uber.org/zap"

// newSource reads the hwmon class and the thermal zones.
func newSource(logger *zap.Logger, cfg *Config) (source, error) {
	return newSysfsSource(logger, cfg.HwmonPath), nil
}
