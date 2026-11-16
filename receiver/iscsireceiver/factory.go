// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"context"
	"errors"
	"runtime"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "iscsi"
	stability = component.StabilityLevelDevelopment
)

// goos is the running system; tests change it.
var goos = runtime.GOOS

// NewFactory creates the factory of the iscsi receiver.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CollectionInterval: time.Minute,
		SysfsPath:          "/sys/class",
		LUNs:               true,
		MaxLUNs:            defaultMaxLUNs,
	}
}

func createMetricsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Metrics,
) (receiver.Metrics, error) {
	if goos != "linux" {
		return nil, errors.New("the iscsi receiver reads the Linux iSCSI sysfs classes and is not available on this platform")
	}
	typed := cfg.(*Config)
	if err := typed.Validate(); err != nil {
		return nil, err
	}
	src := newSysfsReader(settings.Logger, typed.SysfsPath)
	return newReceiver(settings, typed, next, src), nil
}
