package semconv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToggleKeysAreTheTwelveMetricNames(t *testing.T) {
	keys := ToggleKeys()
	assert.Len(t, keys, 12)
	seen := map[string]bool{}
	for _, k := range keys {
		assert.False(t, seen[k], "duplicate key %s", k)
		seen[k] = true
	}
	for _, name := range []string{
		MetricUtilization, MetricMemoryUsage, MetricMemoryLimit,
		MetricMemoryUtilization, MetricIO, MetricErrors, MetricStatus,
		MetricTemperature, MetricPower, MetricClock, MetricPowerLimit,
		MetricPCIeThroughput,
	} {
		assert.True(t, seen[name], "missing %s", name)
	}
}

func TestExtensionsAreToggleKeys(t *testing.T) {
	ext := Extensions()
	assert.Equal(t,
		[]string{"hw.gpu.clock", "hw.gpu.power.limit", "hw.gpu.pcie.throughput"}, ext)
	keys := map[string]bool{}
	for _, k := range ToggleKeys() {
		keys[k] = true
	}
	for _, e := range ext {
		assert.True(t, keys[e])
	}
}
