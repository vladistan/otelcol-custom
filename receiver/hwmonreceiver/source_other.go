// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !freebsd

package hwmonreceiver

import (
	"errors"

	"go.uber.org/zap"
)

// newSource fails off Linux and FreeBSD: there is no sensor interface to read.
func newSource(*zap.Logger, *Config) (source, error) {
	return nil, errors.New("the hwmon receiver reads sysfs or sysctl and is available on Linux or FreeBSD only")
}
