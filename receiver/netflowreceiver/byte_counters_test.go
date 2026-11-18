// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"
	"github.com/netsampler/goflow2/v2/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.uber.org/zap"
)

// The two packets of testdata come from an OPNsense (ng_netflow) NetFlow v9 export. The template
// lists IN_PKTS, IN_BYTES, OUT_PKTS and OUT_BYTES in that order, and the exporter fills the OUT
// counters with 0. Only the addresses are replaced, with documentation addresses.

// loadHex reads the hex dump testdata/<name> and returns its bytes.
func loadHex(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	return out
}

type counters struct{ bytes, packets int64 }

func decodePackets(t *testing.T, payloads ...[]byte) []counters {
	t.Helper()
	sink := new(consumertest.LogsSink)
	nr := &netflowReceiver{config: Config{Scheme: "netflow"}, logger: zap.NewNop(), logConsumer: sink}
	decode, err := nr.buildDecodeFunc()
	require.NoError(t, err)
	src := netip.MustParseAddrPort("192.0.2.1:2055")
	for _, payload := range payloads {
		require.NoError(t, decode(&utils.Message{Src: src, Payload: payload, Received: time.Unix(1, 0)}))
	}
	var out []counters
	for _, logs := range sink.AllLogs() {
		for i := 0; i < logs.ResourceLogs().Len(); i++ {
			for j := 0; j < logs.ResourceLogs().At(i).ScopeLogs().Len(); j++ {
				records := logs.ResourceLogs().At(i).ScopeLogs().At(j).LogRecords()
				for k := 0; k < records.Len(); k++ {
					attrs := records.At(k).Attributes()
					b, _ := attrs.Get("flow.io.bytes")
					p, _ := attrs.Get("flow.io.packets")
					out = append(out, counters{bytes: b.Int(), packets: p.Int()})
				}
			}
		}
	}
	return out
}

func TestOPNsenseExportKeepsBytesAndPackets(t *testing.T) {
	got := decodePackets(t, loadHex(t, "nfv9-opnsense-template.hex"), loadHex(t, "nfv9-opnsense-data.hex"))

	assert.Equal(t, []counters{
		{256, 1}, {52, 1}, {274, 2},
		{216, 4}, {376, 7}, {52, 1},
	}, got)
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func v9Record(fields ...netflow.DataField) *netflow.NFv9Packet {
	return &netflow.NFv9Packet{
		Version: 9, Count: 1, SourceId: 1,
		FlowSets: []any{netflow.DataFlowSet{
			FlowSetHeader: netflow.FlowSetHeader{Id: 256},
			Records:       []netflow.DataRecord{{Values: fields}},
		}},
	}
}

func field(typ uint16, value []byte) netflow.DataField {
	return netflow.DataField{Type: typ, Value: value}
}

func produceOne(t *testing.T, msg any) *protoproducer.ProtoProducerMessage {
	t.Helper()
	compiled, err := (&protoproducer.ProducerConfig{}).Compile()
	require.NoError(t, err)
	inner, err := protoproducer.CreateProtoProducer(compiled, protoproducer.CreateSamplingSystem)
	require.NoError(t, err)
	messages, err := newOtelLogsProducer(inner, consumertest.NewNop(), zap.NewNop(), false).
		Produce(msg, &producer.ProduceArgs{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	pm, ok := messages[0].(*protoproducer.ProtoProducerMessage)
	require.True(t, ok)
	return pm
}

func TestV9AddsInAndOutCountersOfOneRecord(t *testing.T) {
	// out first and in last: neither order may decide the result
	for name, fields := range map[string][]netflow.DataField{
		"in then out": {
			field(netflow.NFV9_FIELD_IN_PKTS, be32(3)), field(netflow.NFV9_FIELD_IN_BYTES, be32(100)),
			field(netflow.NFV9_FIELD_OUT_PKTS, be32(2)), field(netflow.NFV9_FIELD_OUT_BYTES, be32(50)),
		},
		"out then in": {
			field(netflow.NFV9_FIELD_OUT_PKTS, be32(2)), field(netflow.NFV9_FIELD_OUT_BYTES, be32(50)),
			field(netflow.NFV9_FIELD_IN_PKTS, be32(3)), field(netflow.NFV9_FIELD_IN_BYTES, be32(100)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			pm := produceOne(t, v9Record(fields...))

			assert.Equal(t, uint64(150), pm.Bytes)
			assert.Equal(t, uint64(5), pm.Packets)
		})
	}
}

func TestV9ZeroOutCountersDoNotEraseTheInCounters(t *testing.T) {
	pm := produceOne(t, v9Record(
		field(netflow.NFV9_FIELD_IN_PKTS, be32(7)), field(netflow.NFV9_FIELD_IN_BYTES, be32(376)),
		field(netflow.NFV9_FIELD_OUT_PKTS, be32(0)), field(netflow.NFV9_FIELD_OUT_BYTES, be32(0)),
	))

	assert.Equal(t, uint64(376), pm.Bytes)
	assert.Equal(t, uint64(7), pm.Packets)
}

func TestV9OutCountersAloneAreStillUsed(t *testing.T) {
	pm := produceOne(t, v9Record(
		field(netflow.NFV9_FIELD_OUT_PKTS, be32(2)), field(netflow.NFV9_FIELD_OUT_BYTES, be32(50)),
	))

	assert.Equal(t, uint64(50), pm.Bytes)
	assert.Equal(t, uint64(2), pm.Packets)
}

func TestIPFIXCountersKeepTheirOldMeaning(t *testing.T) {
	// IPFIX type 23 and 24 are the counters after a middlebox (post NAT) of the same traffic,
	// so adding them to the type 1 and 2 counters would count the traffic twice.
	msg := &netflow.IPFIXPacket{
		Version: 10, ObservationDomainId: 1,
		FlowSets: []any{netflow.DataFlowSet{
			FlowSetHeader: netflow.FlowSetHeader{Id: 256},
			Records: []netflow.DataRecord{{Values: []netflow.DataField{
				field(netflow.NFV9_FIELD_IN_PKTS, be32(3)), field(netflow.NFV9_FIELD_IN_BYTES, be32(100)),
				field(netflow.NFV9_FIELD_OUT_PKTS, be32(2)), field(netflow.NFV9_FIELD_OUT_BYTES, be32(90)),
			}}},
		}},
	}

	pm := produceOne(t, msg)

	assert.Equal(t, uint64(90), pm.Bytes)
	assert.Equal(t, uint64(2), pm.Packets)
}
