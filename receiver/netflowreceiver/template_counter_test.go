// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/netsampler/goflow2/v2/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver/internal/metadata"
)

// templateCounterName is the counter of data records dropped because their template is not known.
const templateCounterName = "otelcol_netflow_template_not_found"

func metricTotal(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

func TestDataBeforeItsTemplate_WritesNoRecordRaisesTheCounterAndIsDecodedAfterTheTemplate(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := receivertest.NewNopSettings(metadata.Type)
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sink := new(consumertest.LogsSink)
	nr, err := newNetflowLogsReceiver(set, Config{Scheme: "netflow", Sockets: 1, Workers: 1, QueueSize: 10, Port: 2055}, sink)
	require.NoError(t, err)
	receiver := nr.(*netflowReceiver)
	decode, err := receiver.buildDecodeFunc()
	require.NoError(t, err)
	src := netip.MustParseAddrPort("192.0.2.1:2055")
	send := func(payload []byte) error {
		return decode(&utils.Message{Src: src, Payload: payload, Received: time.Unix(1, 0)})
	}

	dataErr := send(loadHex(t, "nfv9-opnsense-data.hex"))
	receiver.noteError(dataErr)

	require.Error(t, dataErr)
	assert.Equal(t, 0, sink.LogRecordCount(), "no row for a record whose template is unknown")
	assert.Equal(t, int64(1), metricTotal(t, reader, templateCounterName))

	require.NoError(t, send(loadHex(t, "nfv9-opnsense-template.hex")))
	require.NoError(t, send(loadHex(t, "nfv9-opnsense-data.hex")))

	assert.Positive(t, sink.LogRecordCount(), "records follow once the template has arrived")
	assert.Equal(t, int64(1), metricTotal(t, reader, templateCounterName))
}

func TestOtherErrors_DoNotRaiseTheTemplateCounter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := receivertest.NewNopSettings(metadata.Type)
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	nr, err := newNetflowLogsReceiver(set, Config{Scheme: "netflow", Sockets: 1, Workers: 1, QueueSize: 10, Port: 2055}, new(consumertest.LogsSink))
	require.NoError(t, err)
	receiver := nr.(*netflowReceiver)
	receiver.logger = zap.NewNop()

	receiver.noteError(assert.AnError)

	assert.Zero(t, metricTotal(t, reader, templateCounterName))
}
