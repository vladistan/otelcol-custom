// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver

import (
	"testing"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.uber.org/zap"
)

// The NetFlow v9 field types of the absolute flow times in milliseconds (the IPFIX numbers
// flowStartMilliseconds and flowEndMilliseconds, which Flexible NetFlow on IOS-XE also uses) and of
// the uptime-relative times.
const (
	typeFlowStartMilliseconds = 152
	typeFlowEndMilliseconds   = 153
	typeFirstSwitched         = netflow.NFV9_FIELD_FIRST_SWITCHED
	typeLastSwitched          = netflow.NFV9_FIELD_LAST_SWITCHED
)

const (
	headerSeconds = 1795129202 // the export time of the packet header
	headerUptime  = 1000000    // the exporter uptime in milliseconds
)

// v9Packet is a packet of one data set; each record is a list of fields.
func v9Packet(records ...[]netflow.DataField) *netflow.NFv9Packet {
	data := netflow.DataFlowSet{FlowSetHeader: netflow.FlowSetHeader{Id: 256}}
	for _, r := range records {
		data.Records = append(data.Records, netflow.DataRecord{Values: r})
	}
	return &netflow.NFv9Packet{
		Version: 9, Count: uint16(len(records)), SourceId: 1,
		UnixSeconds: headerSeconds, SystemUptime: headerUptime,
		FlowSets: []any{data},
	}
}

func produceAll(t *testing.T, msg any) []*protoproducer.ProtoProducerMessage {
	t.Helper()
	compiled, err := (&protoproducer.ProducerConfig{}).Compile()
	require.NoError(t, err)
	inner, err := protoproducer.CreateProtoProducer(compiled, protoproducer.CreateSamplingSystem)
	require.NoError(t, err)
	messages, err := newOtelLogsProducer(inner, consumertest.NewNop(), zap.NewNop(), false).
		Produce(msg, &producer.ProduceArgs{})
	require.NoError(t, err)
	out := make([]*protoproducer.ProtoProducerMessage, 0, len(messages))
	for _, m := range messages {
		pm, ok := m.(*protoproducer.ProtoProducerMessage)
		require.True(t, ok)
		out = append(out, pm)
	}
	return out
}

const (
	startMs = 1795129200123 // 2.877 s before the header time
	endMs   = 1795129201456
)

func TestV9AbsoluteMillisecondTimes_GiveTheStartAndEndOfTheFlow(t *testing.T) {
	pm := produceAll(t, v9Packet([]netflow.DataField{
		field(typeFlowStartMilliseconds, be64(startMs)), field(typeFlowEndMilliseconds, be64(endMs)),
	}))[0]

	assert.Equal(t, uint64(startMs)*1e6, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(endMs)*1e6, pm.TimeFlowEndNs)
}

func TestV9AbsoluteTimes_DoNotNeedTheExporterUptime(t *testing.T) {
	// the IOS-XE export has a header uptime of 0
	msg := v9Packet([]netflow.DataField{
		field(typeFlowStartMilliseconds, be64(startMs)), field(typeFlowEndMilliseconds, be64(endMs)),
	})
	msg.SystemUptime = 0

	pm := produceAll(t, msg)[0]

	assert.Equal(t, uint64(startMs)*1e6, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(endMs)*1e6, pm.TimeFlowEndNs)
}

func TestV9RelativeTimes_StillUseTheHeaderAndTheUptime(t *testing.T) {
	// first switched one second and last switched half a second before the export
	pm := produceAll(t, v9Packet([]netflow.DataField{
		field(typeFirstSwitched, be32(headerUptime-1000)), field(typeLastSwitched, be32(headerUptime-500)),
	}))[0]

	assert.Equal(t, uint64(headerSeconds)*1e9-1000*1e6, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(headerSeconds)*1e9-500*1e6, pm.TimeFlowEndNs)
}

func TestV9BothTimeForms_TheAbsoluteOneWins(t *testing.T) {
	pm := produceAll(t, v9Packet([]netflow.DataField{
		field(typeFirstSwitched, be32(headerUptime-1000)), field(typeLastSwitched, be32(headerUptime-500)),
		field(typeFlowStartMilliseconds, be64(startMs)), field(typeFlowEndMilliseconds, be64(endMs)),
	}))[0]

	assert.Equal(t, uint64(startMs)*1e6, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(endMs)*1e6, pm.TimeFlowEndNs)
}

func TestV9RecordWithNoTime_HasStartAndEndZeroNotTheExportTime(t *testing.T) {
	pm := produceAll(t, v9Packet([]netflow.DataField{
		field(netflow.NFV9_FIELD_IN_PKTS, be32(1)), field(netflow.NFV9_FIELD_IN_BYTES, be32(52)),
	}))[0]

	assert.Zero(t, pm.TimeFlowStartNs)
	assert.Zero(t, pm.TimeFlowEndNs)
}

func TestV9AbsoluteTimeOutsideTheCalendar_GivesZero(t *testing.T) {
	pm := produceAll(t, v9Packet([]netflow.DataField{
		field(typeFlowStartMilliseconds, be64(5)), field(typeFlowEndMilliseconds, be64(endMs)),
	}))[0]

	assert.Zero(t, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(endMs)*1e6, pm.TimeFlowEndNs)
}

func TestV9TwoRecords_EachGetItsOwnTimes(t *testing.T) {
	got := produceAll(t, v9Packet(
		[]netflow.DataField{field(typeFlowStartMilliseconds, be64(startMs)), field(typeFlowEndMilliseconds, be64(endMs))},
		[]netflow.DataField{field(netflow.NFV9_FIELD_IN_BYTES, be32(9))},
		[]netflow.DataField{field(typeFlowStartMilliseconds, be64(startMs+10)), field(typeFlowEndMilliseconds, be64(endMs+10))},
	))

	require.Len(t, got, 3)
	assert.Equal(t, uint64(startMs)*1e6, got[0].TimeFlowStartNs)
	assert.Zero(t, got[1].TimeFlowStartNs)
	assert.Equal(t, uint64(startMs+10)*1e6, got[2].TimeFlowStartNs)
}

func TestIPFIXTimes_AreLeftToTheProducer(t *testing.T) {
	msg := &netflow.IPFIXPacket{
		Version: 10, ObservationDomainId: 1, ExportTime: headerSeconds,
		FlowSets: []any{netflow.DataFlowSet{
			FlowSetHeader: netflow.FlowSetHeader{Id: 256},
			Records: []netflow.DataRecord{{Values: []netflow.DataField{
				field(typeFlowStartMilliseconds, be64(startMs)), field(typeFlowEndMilliseconds, be64(endMs)),
			}}},
		}},
	}

	pm := produceAll(t, msg)[0]

	assert.Equal(t, uint64(startMs)*1e6, pm.TimeFlowStartNs)
	assert.Equal(t, uint64(endMs)*1e6, pm.TimeFlowEndNs)
}
