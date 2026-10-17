// Modified by Vlad Korolev, 2026-10: port constructor injected; stub-platform and zero-port-call tests added.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/factory_test.go

package gpureceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/nvmladapter"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func recordingFactory(p *recordingPort) func() (port.Port, error) {
	return func() (port.Port, error) { return p, nil }
}

func TestNewFactory(t *testing.T) {
	f := newFactory(recordingFactory(&recordingPort{}), newEmptyCollector)

	assert.Equal(t, typeStr, f.Type())
	assert.NotNil(t, f.CreateDefaultConfig())
}

// AC-022: the default config has a 10s interval and every metric enabled.
func TestFactoryDefaultConfig(t *testing.T) {
	cfg, ok := NewFactory().CreateDefaultConfig().(*Config)
	require.True(t, ok)

	assert.Equal(t, 10*time.Second, cfg.CollectionInterval)
	require.Len(t, cfg.Metrics, 12)
	for key, metric := range cfg.Metrics {
		assert.True(t, metric.Enabled, key)
	}
}

// AC-023: create, Start and Shutdown make zero port calls.
func TestCreateStartShutdownMakePortCallsNone(t *testing.T) {
	p := &recordingPort{}
	f := newFactory(recordingFactory(p), newEmptyCollector)
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.InitialDelay = time.Hour

	r, err := f.CreateMetrics(
		context.Background(), receivertest.NewNopSettings(typeStr), cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, r.Shutdown(context.Background()))

	assert.Zero(t, p.calls())
}

func TestCreateMetricsReturnsPortConstructorError(t *testing.T) {
	f := newFactory(func() (port.Port, error) { return nil, nvmladapter.ErrPlatformUnsupported }, newEmptyCollector)

	r, err := f.CreateMetrics(
		context.Background(), receivertest.NewNopSettings(typeStr), f.CreateDefaultConfig(), consumertest.NewNop())

	assert.ErrorIs(t, err, nvmladapter.ErrPlatformUnsupported)
	assert.Nil(t, r)
}
