// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "pve"
	stability = component.StabilityLevelDevelopment
)

// instances holds one receiver per configuration, so the metrics and the logs pipeline share it.
var instances = struct {
	mu sync.Mutex
	m  map[*Config]*pveReceiver
}{m: map[*Config]*pveReceiver{}}

// NewFactory creates the factory of the pve receiver.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetrics, stability),
		receiver.WithLogs(createLogs, stability),
	)
}

func sharedReceiver(settings receiver.Settings, cfg component.Config) (*pveReceiver, error) {
	rcfg := cfg.(*Config)
	if err := rcfg.Validate(); err != nil {
		return nil, err
	}
	instances.mu.Lock()
	defer instances.mu.Unlock()
	if r, ok := instances.m[rcfg]; ok {
		return r, nil
	}
	r, err := newReceiver(settings, rcfg)
	if err != nil {
		return nil, err
	}
	instances.m[rcfg] = r
	return r, nil
}

func createMetrics(_ context.Context, settings receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
	r, err := sharedReceiver(settings, cfg)
	if err != nil {
		return nil, err
	}
	r.setMetrics(next)
	return r, nil
}

func createLogs(_ context.Context, settings receiver.Settings, cfg component.Config, next consumer.Logs) (receiver.Logs, error) {
	r, err := sharedReceiver(settings, cfg)
	if err != nil {
		return nil, err
	}
	r.setLogs(next)
	return r, nil
}

func forget(cfg *Config) {
	instances.mu.Lock()
	defer instances.mu.Unlock()
	delete(instances.m, cfg)
}
