// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package hwmonreceiver

import "go.uber.org/zap"

// newSource reads the CPU and ACPI temperature sysctls; hwmon_path is not used.
func newSource(logger *zap.Logger, _ *Config) (source, error) {
	return newSysctlSource(logger, unixSysctl{}), nil
}
