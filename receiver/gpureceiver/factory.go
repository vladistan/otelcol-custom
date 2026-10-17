// Modified by Vlad Korolev, 2026-10: NVML port constructor and collector builder injected; AddScraper for v0.140.0.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/factory.go

package gpureceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/scraper"
	"go.opentelemetry.io/collector/scraper/scraperhelper"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/collect"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/nvmladapter"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

// collectorBuilder builds the per-scrape Collector once devices are enumerated.
type collectorBuilder func(
	p port.Port,
	devices []port.DeviceIdentity,
	toggles port.Toggles,
	start time.Time,
	logger *zap.Logger,
) port.Collector

type emptyCollector struct{}

func (emptyCollector) Collect(time.Time) pmetric.Metrics { return pmetric.NewMetrics() }

func newEmptyCollector(
	port.Port, []port.DeviceIdentity, port.Toggles, time.Time, *zap.Logger,
) port.Collector {
	return emptyCollector{}
}

// NewFactory wires the NVML adapter; tests call newFactory with a fake port.
func NewFactory() receiver.Factory {
	return newFactoryFor(nvmladapter.NewPort, collectBuilderFor)
}

// collectBuilderFor passes the configured interval to collect.New, whose
// variadic options do not fit the collectorBuilder signature directly.
func collectBuilderFor(cfg *Config) collectorBuilder {
	return func(
		p port.Port,
		devices []port.DeviceIdentity,
		toggles port.Toggles,
		start time.Time,
		logger *zap.Logger,
	) port.Collector {
		return collect.New(p, devices, toggles, start, logger,
			collect.WithInterval(cfg.CollectionInterval))
	}
}

func newFactory(newPort func() (port.Port, error), build collectorBuilder) receiver.Factory {
	return newFactoryFor(newPort, func(*Config) collectorBuilder { return build })
}

func newFactoryFor(
	newPort func() (port.Port, error),
	builderFor func(*Config) collectorBuilder,
) receiver.Factory {
	return receiver.NewFactory(
		typeStr,
		func() component.Config {
			return createDefaultConfig()
		},
		receiver.WithMetrics(createMetricsReceiverFunc(newPort, builderFor), MetricsStability),
	)
}

func createMetricsReceiverFunc(
	newPort func() (port.Port, error),
	builderFor func(*Config) collectorBuilder,
) receiver.CreateMetricsFunc {
	return func(
		_ context.Context,
		settings receiver.Settings,
		cfg component.Config,
		nextConsumer consumer.Metrics,
	) (receiver.Metrics, error) {
		gpuCfg := cfg.(*Config)
		p, err := newPort()
		if err != nil {
			return nil, err
		}
		s := newGPUScraper(settings.Logger, gpuCfg, p, builderFor(gpuCfg))

		gpuScraper, err := scraper.NewMetrics(
			s.scrape,
			scraper.WithStart(s.start),
			scraper.WithShutdown(s.shutdown),
		)
		if err != nil {
			return nil, err
		}

		return scraperhelper.NewMetricsController(
			&gpuCfg.ControllerConfig,
			settings,
			nextConsumer,
			scraperhelper.AddScraper(typeStr, gpuScraper),
		)
	}
}
