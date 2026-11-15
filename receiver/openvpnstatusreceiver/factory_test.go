package openvpnstatusreceiver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestFactoryTypeIsOpenvpnstatus(t *testing.T) {
	assert.Equal(t, "openvpnstatus", NewFactory().Type().String())
}

func TestFactoryDefaultConfigIsAValidStructure(t *testing.T) {
	require.NoError(t, componenttest.CheckConfigStruct(NewFactory().CreateDefaultConfig()))
}

func TestFactoryCreatesTheSignalsTheOutputNames(t *testing.T) {
	tests := map[string]struct{ metrics, logs bool }{
		"":        {true, true},
		"metrics": {true, false},
		"logs":    {false, true},
		"both":    {true, true},
	}
	for output, want := range tests {
		t.Run("output="+output, func(t *testing.T) {
			f := NewFactory()
			settings := receivertest.NewNopSettings(f.Type())
			cfg := f.CreateDefaultConfig().(*Config)
			cfg.Output = output

			m, metricsErr := f.CreateMetrics(context.Background(), settings, cfg, consumertest.NewNop())
			l, logsErr := f.CreateLogs(context.Background(), settings, cfg, consumertest.NewNop())

			assert.Equal(t, want.metrics, metricsErr == nil && m != nil)
			assert.Equal(t, want.logs, logsErr == nil && l != nil)
		})
	}
}

func TestFactoryRefusalOfAnExplicitOutputNamesTheSetting(t *testing.T) {
	f := NewFactory()
	settings := receivertest.NewNopSettings(f.Type())
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.Output = outputMetrics

	_, err := f.CreateLogs(context.Background(), settings, cfg, consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), `output is "metrics"`)
	assert.Contains(t, err.Error(), `"logs" or "both"`)

	cfg.Output = outputLogs
	_, err = f.CreateMetrics(context.Background(), settings, cfg, consumertest.NewNop())

	require.Error(t, err)
	assert.Contains(t, err.Error(), `output is "logs"`)
}

// A 0.3.13 config has the receiver in a logs pipeline and no output key.
func TestFactoryUnsetOutputKeepsA0313LogsConfigWorking(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.Path = filepath.Join(t.TempDir(), "status.log")
	require.NoError(t, os.WriteFile(cfg.Path, readFixture(t, "v1-two-clients.txt"), 0o600))
	cfg.PollInterval = 10 * time.Millisecond
	sink := new(consumertest.LogsSink)

	r, err := f.CreateLogs(context.Background(), receivertest.NewNopSettings(f.Type()), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	require.Eventually(t, func() bool { return sink.LogRecordCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestFactoryOutputBothFeedsALogsAndAMetricsPipeline(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.Path = filepath.Join(t.TempDir(), "status.log")
	require.NoError(t, os.WriteFile(cfg.Path, readFixture(t, "v1-two-clients.txt"), 0o600))
	cfg.PollInterval = 10 * time.Millisecond
	cfg.Output = outputBoth
	settings := receivertest.NewNopSettings(f.Type())
	logs, metrics := new(consumertest.LogsSink), new(consumertest.MetricsSink)

	lr, err := f.CreateLogs(context.Background(), settings, cfg, logs)
	require.NoError(t, err)
	mr, err := f.CreateMetrics(context.Background(), settings, cfg, metrics)
	require.NoError(t, err)
	require.NoError(t, lr.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, mr.Start(context.Background(), componenttest.NewNopHost()))

	require.Eventually(t, func() bool {
		return logs.LogRecordCount() == 2 && metrics.DataPointCount() == 6
	}, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, lr.Shutdown(context.Background()))
	require.NoError(t, mr.Shutdown(context.Background()))
}
