package openvpnstatusreceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	typeStr   = "openvpnstatus"
	stability = component.StabilityLevelDevelopment

	// The OpenVPN server rewrites the status file every 20 s by default.
	defaultPollInterval = 20 * time.Second
)

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		receiver.WithLogs(createLogsReceiver, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{PollInterval: defaultPollInterval}
}

func createLogsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Logs,
) (receiver.Logs, error) {
	return newStatusReceiver(settings, cfg.(*Config), next), nil
}
