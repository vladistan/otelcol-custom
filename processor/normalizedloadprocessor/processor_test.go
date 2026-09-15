package normalizedloadprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestNormalizedLoadProcessor(t *testing.T) {
	// Create test metrics with load averages
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()

	// Add load average metrics (simulating 8 CPU system with load of 4.0)
	addGaugeMetric(sm, "system.cpu.load_average.1m", 4.0)
	addGaugeMetric(sm, "system.cpu.load_average.5m", 8.0)
	addGaugeMetric(sm, "system.cpu.load_average.15m", 16.0)
	addGaugeMetric(sm, "system.cpu.utilization", 50.0) // Should be unchanged

	// Create processor with 8 CPUs
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings()
	cfg := &Config{CPUCount: 8}

	proc, err := newNormalizedLoadProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Process metrics
	err = proc.ConsumeMetrics(context.Background(), md)
	require.NoError(t, err)

	// Verify output
	require.Greater(t, sink.DataPointCount(), 0, "Expected metrics in sink")
	outputMD := sink.AllMetrics()[0]

	// Find the normalized metrics
	metrics := outputMD.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()

	// Should have original 4 + 3 normalized = 7 metrics
	assert.Equal(t, 7, metrics.Len(), "Expected 7 metrics (4 original + 3 normalized)")

	// Verify normalized values
	normalizedMetrics := make(map[string]float64)
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if m.Type() == pmetric.MetricTypeGauge && m.Gauge().DataPoints().Len() > 0 {
			normalizedMetrics[m.Name()] = m.Gauge().DataPoints().At(0).DoubleValue()
		}
	}

	// Original metrics should be unchanged
	assert.Equal(t, 4.0, normalizedMetrics["system.cpu.load_average.1m"])
	assert.Equal(t, 8.0, normalizedMetrics["system.cpu.load_average.5m"])
	assert.Equal(t, 16.0, normalizedMetrics["system.cpu.load_average.15m"])
	assert.Equal(t, 50.0, normalizedMetrics["system.cpu.utilization"])

	// Normalized metrics should be load / cpuCount
	assert.Equal(t, 0.5, normalizedMetrics["system.cpu.load_average.normalized.1m"])
	assert.Equal(t, 1.0, normalizedMetrics["system.cpu.load_average.normalized.5m"])
	assert.Equal(t, 2.0, normalizedMetrics["system.cpu.load_average.normalized.15m"])
}

func TestNormalizedLoadProcessorAutoDetect(t *testing.T) {
	// Create processor with auto-detect (CPUCount = 0)
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings()
	cfg := &Config{CPUCount: 0}

	proc, err := newNormalizedLoadProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Should have detected some CPUs
	assert.Greater(t, proc.cpuCount, 0, "Expected auto-detected CPU count > 0")
}

func TestNormalizedLoadProcessorNoLoadMetrics(t *testing.T) {
	// Create test metrics without load averages
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()

	addGaugeMetric(sm, "system.cpu.utilization", 50.0)
	addGaugeMetric(sm, "system.memory.usage", 1024.0)

	// Create processor
	sink := &consumertest.MetricsSink{}
	settings := processortest.NewNopSettings()
	cfg := &Config{CPUCount: 4}

	proc, err := newNormalizedLoadProcessor(settings, cfg, sink)
	require.NoError(t, err)

	// Process metrics
	err = proc.ConsumeMetrics(context.Background(), md)
	require.NoError(t, err)

	// Verify output - should have same number of metrics (no normalized added)
	outputMD := sink.AllMetrics()[0]
	metrics := outputMD.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	assert.Equal(t, 2, metrics.Len(), "Expected 2 metrics (no load averages)")
}

func addGaugeMetric(sm pmetric.ScopeMetrics, name string, value float64) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	g := m.SetEmptyGauge()
	dp := g.DataPoints().AppendEmpty()
	dp.SetDoubleValue(value)
}
