// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netsampler/goflow2/v2/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// testdata/switch-netflow-v9.scrubbed.pcap is a capture of a Cisco Catalyst switch that exports
// Flexible NetFlow v9: an IPv4 template (256) and an IPv6 template (257), each with the absolute
// flow times in milliseconds (types 152 and 153) and no uptime-relative times, no TOS and no AS.
// The header uptime is 0 and the header clock runs about two seconds ahead of the capture clock.
// The addresses are replaced with documentation addresses.

// capturePayloads returns the UDP payloads of a legacy pcap file with Ethernet frames, and the
// capture time of each.
func capturePayloads(t *testing.T, name string) (payloads [][]byte, times []time.Time) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.Greater(t, len(raw), 24)
	require.Equal(t, uint32(0xa1b2c3d4), binary.LittleEndian.Uint32(raw[:4]), "little-endian microsecond pcap")
	require.Equal(t, uint32(1), binary.LittleEndian.Uint32(raw[20:24]), "Ethernet link type")
	for off := 24; off+16 <= len(raw); {
		sec := binary.LittleEndian.Uint32(raw[off:])
		size := int(binary.LittleEndian.Uint32(raw[off+8:]))
		off += 16
		frame := raw[off : off+size]
		off += size
		ihl := int(frame[14]&0x0f) * 4
		payloads = append(payloads, frame[14+ihl+8:])
		times = append(times, time.Unix(int64(sec), 0))
	}
	return payloads, times
}

// decodeRecords runs the payloads through the receiver and returns the log records.
func decodeRecords(t *testing.T, payloads [][]byte) []plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	nr := &netflowReceiver{config: Config{Scheme: "netflow"}, logger: zap.NewNop(), logConsumer: sink}
	decode, err := nr.buildDecodeFunc()
	require.NoError(t, err)
	src := netip.MustParseAddrPort("192.0.2.10:2055")
	for _, payload := range payloads {
		require.NoError(t, decode(&utils.Message{Src: src, Payload: payload, Received: time.Now()}))
	}
	var out []plog.LogRecord
	for _, logs := range sink.AllLogs() {
		for i := range logs.ResourceLogs().Len() {
			scopes := logs.ResourceLogs().At(i).ScopeLogs()
			for j := range scopes.Len() {
				records := scopes.At(j).LogRecords()
				for k := range records.Len() {
					out = append(out, records.At(k))
				}
			}
		}
	}
	return out
}

func intAttr(t *testing.T, r plog.LogRecord, key string) int64 {
	t.Helper()
	v, ok := r.Attributes().Get(key)
	require.True(t, ok, "%s is missing", key)
	return v.Int()
}

func strAttr(t *testing.T, r plog.LogRecord, key string) string {
	t.Helper()
	v, ok := r.Attributes().Get(key)
	require.True(t, ok, "%s is missing", key)
	return v.Str()
}

func TestSwitchCapture_EveryRecordHasItsOwnTimesAndCounters(t *testing.T) {
	payloads, _ := capturePayloads(t, "switch-netflow-v9.scrubbed.pcap")

	records := decodeRecords(t, payloads)

	require.NotEmpty(t, records)
	for i, r := range records {
		start, end := intAttr(t, r, "flow.start"), intAttr(t, r, "flow.end")
		assert.Positive(t, start, "record %d has no start", i)
		assert.GreaterOrEqual(t, end, start, "record %d ends before it starts", i)
		assert.Less(t, end-start, int64(time.Hour), "record %d lasts more than an hour", i)
		assert.Positive(t, intAttr(t, r, "flow.io.bytes"), "record %d has no bytes", i)
		assert.Positive(t, intAttr(t, r, "flow.io.packets"), "record %d has no packets", i)
		assert.Equal(t, "netflow_v9", strAttr(t, r, "flow.type"))
		assert.Equal(t, "192.0.2.10", strAttr(t, r, "flow.sampler_address"))
	}
}

func TestSwitchCapture_TheStartIsTheFlowTimeAndNotTheExportTime(t *testing.T) {
	payloads, captured := capturePayloads(t, "switch-netflow-v9.scrubbed.pcap")

	// The header clock runs ahead of the capture clock, so a start taken from the header would sit
	// after the capture time of its own packet; the start of a flow that was exported does not.
	// Every packet of the capture carries its template, so each decodes alone.
	var checked int
	for i, p := range payloads {
		for _, r := range decodeRecords(t, [][]byte{p}) {
			start := intAttr(t, r, "flow.start")
			assert.LessOrEqual(t, start, captured[i].UnixNano(), "packet %d: the flow starts after it was exported", i)
			assert.Greater(t, start, captured[i].UnixNano()-int64(time.Hour), "packet %d: the flow starts an hour before", i)
			checked++
		}
	}
	assert.Positive(t, checked)
}

func TestSwitchCapture_IPv6RecordsUnderTheirOwnTemplateHaveAddressesAndCounters(t *testing.T) {
	payloads, _ := capturePayloads(t, "switch-netflow-v9.scrubbed.pcap")

	records := decodeRecords(t, payloads)

	var v4, v6 int
	for _, r := range records {
		src, err := netip.ParseAddr(strAttr(t, r, "source.address"))
		require.NoError(t, err)
		dst, err := netip.ParseAddr(strAttr(t, r, "destination.address"))
		require.NoError(t, err)
		if src.Is6() {
			v6++
			assert.True(t, dst.Is6(), "an IPv6 flow has an IPv6 destination")
			assert.Positive(t, intAttr(t, r, "flow.io.bytes"))
			assert.Positive(t, intAttr(t, r, "flow.io.packets"))
		} else {
			v4++
		}
	}
	assert.Positive(t, v4, "the capture holds IPv4 flows")
	assert.Positive(t, v6, "the capture holds IPv6 flows")
}
