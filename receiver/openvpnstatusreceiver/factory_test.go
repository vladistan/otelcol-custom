package openvpnstatusreceiver

import (
	"context"
	"testing"

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

func TestFactoryCreatesALogsReceiverOnly(t *testing.T) {
	f := NewFactory()
	settings := receivertest.NewNopSettings(f.Type())
	cfg := f.CreateDefaultConfig()

	logsReceiver, err := f.CreateLogs(context.Background(), settings, cfg, consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, logsReceiver)

	_, err = f.CreateMetrics(context.Background(), settings, cfg, consumertest.NewNop())
	assert.Error(t, err)
}
