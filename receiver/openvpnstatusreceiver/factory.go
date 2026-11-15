package openvpnstatusreceiver

import (
	"context"
	"fmt"
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
		receiver.WithMetrics(createMetricsReceiver, stability),
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
	typed := cfg.(*Config)
	if typed.Output == outputMetrics {
		return nil, refusal(typed.Output, "logs")
	}
	return newStatusReceiver(settings, typed, next, nil), nil
}

func createMetricsReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	next consumer.Metrics,
) (receiver.Metrics, error) {
	typed := cfg.(*Config)
	if typed.Output == outputLogs {
		return nil, refusal(typed.Output, "metrics")
	}
	return newStatusReceiver(settings, typed, nil, next), nil
}

// refusal explains why a pipeline of one signal cannot use this receiver.
func refusal(output, signal string) error {
	return fmt.Errorf("openvpnstatus: output is %q, so the receiver emits no %s; set output to %q or %q to use it in a %s pipeline",
		output, signal, signal, outputBoth, signal)
}
