// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestNewFactoryTypeIsCloudwatchmetrics(t *testing.T) {
	assert.Equal(t, "cloudwatchmetrics", NewFactory().Type().String())
}

func TestFactoryCreatesAMetricsReceiverOnly(t *testing.T) {
	f := NewFactory()
	set := receivertest.NewNopSettings(f.Type())

	r, err := f.CreateMetrics(context.Background(), set, validConfig(), consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, r)

	_, err = f.CreateLogs(context.Background(), set, validConfig(), consumertest.NewNop())
	assert.Error(t, err)
	_, err = f.CreateTraces(context.Background(), set, validConfig(), consumertest.NewNop())
	assert.Error(t, err)
}
