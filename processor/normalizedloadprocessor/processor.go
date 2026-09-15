package normalizedloadprocessor

import (
	"context"
	"runtime"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

// normalizedLoadProcessor creates normalized load average metrics.
type normalizedLoadProcessor struct {
	logger       *zap.Logger
	nextConsumer consumer.Metrics
	cpuCount     int
}

// Metrics to normalize (divide by CPU count)
var loadMetricMappings = map[string]string{
	"system.cpu.load_average.1m":  "system.cpu.load_average.normalized.1m",
	"system.cpu.load_average.5m":  "system.cpu.load_average.normalized.5m",
	"system.cpu.load_average.15m": "system.cpu.load_average.normalized.15m",
}

// Metrics to denormalize (multiply by CPU count) - these are already normalized by OTel
// and we want top-style single-core percentages
var denormalizeMetricMappings = map[string]string{
	"process.cpu.utilization": "process.cpu.utilization.percpu",
	"system.cpu.utilization":  "system.cpu.utilization.percpu",
}

func newNormalizedLoadProcessor(
	settings processor.Settings,
	cfg *Config,
	nextConsumer consumer.Metrics,
) (*normalizedLoadProcessor, error) {
	cpuCount := cfg.CPUCount
	if cpuCount <= 0 {
		cpuCount = runtime.NumCPU()
	}

	settings.Logger.Info("Normalized load processor initialized",
		zap.Int("cpu_count", cpuCount))

	return &normalizedLoadProcessor{
		logger:       settings.Logger,
		nextConsumer: nextConsumer,
		cpuCount:     cpuCount,
	}, nil
}

func (p *normalizedLoadProcessor) Start(ctx context.Context, host component.Host) error {
	return nil
}

func (p *normalizedLoadProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *normalizedLoadProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *normalizedLoadProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		sms := rm.ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			sm := sms.At(j)
			p.processMetrics(sm)
		}
	}

	return p.nextConsumer.ConsumeMetrics(ctx, md)
}

func (p *normalizedLoadProcessor) processMetrics(sm pmetric.ScopeMetrics) {
	metrics := sm.Metrics()
	var newMetrics []pmetric.Metric

	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)

		// Check if this metric should be normalized (divide by CPU count)
		if normalizedName, ok := loadMetricMappings[m.Name()]; ok {
			normalized := pmetric.NewMetric()
			m.CopyTo(normalized)
			normalized.SetName(normalizedName)
			normalized.SetDescription("Normalized load average (load / CPU count)")

			switch normalized.Type() {
			case pmetric.MetricTypeGauge:
				dps := normalized.Gauge().DataPoints()
				for k := 0; k < dps.Len(); k++ {
					dp := dps.At(k)
					dp.SetDoubleValue(dp.DoubleValue() / float64(p.cpuCount))
				}
			}

			newMetrics = append(newMetrics, normalized)
		}

		// Check if this metric should be denormalized (multiply by CPU count)
		// This converts OTel's normalized utilization to top-style single-core ratio
		if denormalizedName, ok := denormalizeMetricMappings[m.Name()]; ok {
			denormalized := pmetric.NewMetric()
			m.CopyTo(denormalized)
			denormalized.SetName(denormalizedName)
			denormalized.SetDescription("CPU utilization as ratio of single core (top-style, multiply by 100 for %)")

			switch denormalized.Type() {
			case pmetric.MetricTypeGauge:
				dps := denormalized.Gauge().DataPoints()
				for k := 0; k < dps.Len(); k++ {
					dp := dps.At(k)
					// Multiply by CPU count only (keep as ratio 0-1)
					dp.SetDoubleValue(dp.DoubleValue() * float64(p.cpuCount))
				}
			}

			newMetrics = append(newMetrics, denormalized)
		}
	}

	for _, nm := range newMetrics {
		destMetric := metrics.AppendEmpty()
		nm.CopyTo(destMetric)
	}
}
