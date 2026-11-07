// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package identityfillprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "identityfill"
	stability = component.StabilityLevelDevelopment
)

// NewFactory returns the factory the builder registers once the manifest lists this module.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		HostName: HostNameConfig{
			Sources: []string{"k8s.node.name", "k8s.cluster.name", "gcp.label.cluster_name"},
		},
		ServiceName: ServiceNameConfig{
			K8sEvent:     "k8s-events",
			K8sPodStatus: "k8s-pod-status",
			GCPAudit:     "gcp-audit",
		},
	}
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Logs,
) (processor.Logs, error) {
	conf := cfg.(*Config)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return &processorImpl{cfg: conf, next: nextConsumer, tel: tel}, nil
}
