// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "megaraid"
	stability = component.StabilityLevelDevelopment
)

// goos is the running system; tests change it.
var goos = runtime.GOOS

// sysRoot is where findControllers reads sysfs; tests point it at a fixture tree.
var sysRoot = "/sys"

// NewFactory returns the factory the builder registers once the manifest lists this module. The
// metrics and the logs receiver of one configuration are the same instance, so one read of the
// controllers feeds both pipelines.
func NewFactory() receiver.Factory {
	f := &factory{receivers: map[*Config]*megaReceiver{}}
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(f.createMetrics, stability),
		receiver.WithLogs(f.createLogs, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CollectionInterval: defaultInterval,
		SmartInterval:      defaultSmartInterval,
		Events: EventsConfig{
			Enabled:      defaultEventsEnabled,
			MinClass:     defaultMinClass,
			MaxPerScrape: defaultMaxPerScrape,
		},
	}
}

type factory struct {
	mu        sync.Mutex
	receivers map[*Config]*megaReceiver
}

func (f *factory) get(settings receiver.Settings, cfg component.Config) (*megaReceiver, error) {
	rcfg := cfg.(*Config)
	if goos != "linux" {
		return nil, fmt.Errorf("the megaraid receiver reads /dev/megaraid_sas_ioctl_node and runs only on Linux, not %s", goos)
	}
	if err := rcfg.Validate(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.receivers[rcfg]; ok {
		return r, nil
	}
	r := newReceiver(settings, rcfg)
	f.receivers[rcfg] = r
	return r, nil
}

func (f *factory) createMetrics(_ context.Context, settings receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
	r, err := f.get(settings, cfg)
	if err != nil {
		return nil, err
	}
	r.metrics = next
	return r, nil
}

func (f *factory) createLogs(_ context.Context, settings receiver.Settings, cfg component.Config, next consumer.Logs) (receiver.Logs, error) {
	r, err := f.get(settings, cfg)
	if err != nil {
		return nil, err
	}
	r.logs = next
	return r, nil
}
