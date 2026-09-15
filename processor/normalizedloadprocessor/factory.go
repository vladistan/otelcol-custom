package normalizedloadprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "normalizedload"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a new factory for the normalized load processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CPUCount: 0,
	}
}

func createMetricsProcessor(
	ctx context.Context,
	settings processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	procCfg := cfg.(*Config)
	return newNormalizedLoadProcessor(settings, procCfg, nextConsumer)
}
