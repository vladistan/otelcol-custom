// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore srcaddr dstaddr asn ipfix sctp onprem

package flowlogexporter

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapResult struct {
	row      row
	ok       bool
	warnings []string
	unknown  []string
}

func mapOne(t *testing.T, cfg *Config, attrs map[string]any) mapResult {
	t.Helper()
	var res mapResult
	m := newMapper(cfg, "gateway-0", func(r string) { res.warnings = append(res.warnings, r) }, func(a string) { res.unknown = append(res.unknown, a) })
	rec := logsOf(attrs).ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	res.row, res.ok = m.mapRecord(rec)
	return res
}

func TestMap_FullRecord_FillsTheColumnsOfTheRule(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{
		"flow.next_hop": "198.51.100.1", "flow.vlan_id": int64(20), "flow.sequence_num": int64(77),
		"flow.src_mac": "00:11:22:33:44:55", "flow.dst_mac": "00:11:22:33:44:66",
		"flow.icmp_type": int64(8), "flow.icmp_code": int64(0), "source.as.number": int64(64500), "destination.as.number": int64(64501),
	}))

	require.True(t, res.ok)
	want := row{
		"cloud": "onprem", "account_id": "site-a", "region": "site-a", "loader": "flowlogexporter", "collector": "gateway-0",
		"exporter": "192.0.2.10", "input_interface": "1", "output_interface": "2", "vlan_id": int32(20),
		"srcaddr": "198.51.100.5", "dstaddr": "203.0.113.9", "srcport": int32(51234), "dstport": int32(443),
		"protocol": int32(6), "packets": int64(3), "bytes": int64(1500), "sampling_rate": int32(100),
		"tcp_flags": int32(24), "flow_direction": "ingress", "dscp": int32(46), "next_hop": "198.51.100.1",
		"src_asn": int64(64500), "dst_asn": int64(64501), "action": "ACCEPT", "flow_type": "netflow_v9",
		"sequence_num": uint32(77), "ip_version": uint8(4), "src_mac": "00:11:22:33:44:55", "dst_mac": "00:11:22:33:44:66",
		"icmp_type": uint8(8), "icmp_code": uint8(0),
		"start_time":  time.Unix(1795129200, 0).UTC(),
		"end_time":    time.Unix(1795129201, 0).UTC(),
		"received_at": time.Unix(1795129202, 0).UTC(),
	}
	assert.Equal(t, want, res.row)
	assert.Empty(t, res.warnings)
}

func TestMap_ProtocolNames_GiveTheIANANumber(t *testing.T) {
	for name, want := range map[string]int32{"tcp": 6, "udp": 17, "icmp": 1, "ipv6-icmp": 58, "gre": 47, "esp": 50, "sctp": 132, "89": 89} {
		res := mapOne(t, testConfig(), flowAttrs(map[string]any{"network.transport": name}))

		assert.Equal(t, want, res.row["protocol"], name)
		assert.Empty(t, res.warnings, name)
	}
}

func TestMap_UnknownProtocol_GivesZeroAndAWarning(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{"network.transport": "unknown"}))

	assert.Equal(t, int32(0), res.row["protocol"])
	assert.Equal(t, []string{warnUnknownProtocol}, res.warnings)
}

func TestMap_TypeOfService_GivesTheDscp(t *testing.T) {
	for tos, want := range map[int64]int32{0: 0, 184: 46, 255: 63} {
		res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.ip_tos": tos}))

		assert.Equal(t, want, res.row["dscp"], tos)
	}
}

func TestMap_SamplingRate_RecordWinsThenRouterThenOne(t *testing.T) {
	cases := []struct {
		name  string
		rate  any
		route int32
		want  int32
	}{
		{"record 512 stays", int64(512), 100, 512},
		{"zero takes the router value", int64(0), 100, 100},
		{"absent takes the router value", nil, 100, 100},
		{"zero with no router value is one", int64(0), 0, 1},
		{"absent with no router value is one", nil, 0, 1},
	}
	for _, c := range cases {
		cfg := testConfig()
		cfg.Routers[0].SamplingRate = c.route
		res := mapOne(t, cfg, flowAttrs(map[string]any{"flow.sampling_rate": c.rate}))

		assert.Equal(t, c.want, res.row["sampling_rate"], c.name)
	}
}

func TestMap_BytesAndPackets_AreNotMultipliedBySamplingRate(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.sampling_rate": int64(512)}))

	assert.Equal(t, int64(1500), res.row["bytes"])
	assert.Equal(t, int64(3), res.row["packets"])
}

func TestMap_ForwardingStatus_GivesTheAction(t *testing.T) {
	for code, want := range map[any]string{int64(64): "ACCEPT", int64(65): "ACCEPT", int64(128): "REJECT", int64(192): "", int64(0): "", nil: ""} {
		res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.forwarding_status": code}))

		assert.Equal(t, want, res.row["action"], code)
	}
}

func TestMap_FlowTypeAndIPVersion(t *testing.T) {
	for _, typ := range []string{"netflow_v5", "netflow_v9", "ipfix"} {
		res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.type": typ}))

		assert.Equal(t, typ, res.row["flow_type"])
	}
	v4 := mapOne(t, testConfig(), flowAttrs(nil))
	v6 := mapOne(t, testConfig(), flowAttrs(map[string]any{"source.address": "2001:db8::1", "destination.address": "2001:db8::2"}))
	assert.Equal(t, uint8(4), v4.row["ip_version"])
	assert.Equal(t, uint8(6), v6.row["ip_version"])
}

func TestMap_Times_AreCutToTheSecond(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(nil))

	assert.Equal(t, time.Unix(1795129200, 0).UTC(), res.row["start_time"])
	assert.Equal(t, time.Unix(1795129201, 0).UTC(), res.row["end_time"])
	assert.Equal(t, time.Unix(1795129202, 0).UTC(), res.row["received_at"])
}

func TestMap_BadTimes_GiveTheReceiveSecondForBothAndAWarning(t *testing.T) {
	for name, over := range map[string]map[string]any{
		"zero start":        {"flow.start": int64(0)},
		"zero end":          {"flow.end": int64(0)},
		"start before 2000": {"flow.start": int64(5000000000)},
		"end after 2100":    {"flow.end": int64(5000000000000000000)},
		"both absent":       {"flow.start": nil, "flow.end": nil},
	} {
		res := mapOne(t, testConfig(), flowAttrs(over))

		assert.Equal(t, time.Unix(1795129202, 0).UTC(), res.row["start_time"], name)
		assert.Equal(t, time.Unix(1795129202, 0).UTC(), res.row["end_time"], name)
		assert.Equal(t, []string{warnBadTime}, res.warnings, name)
	}
}

func TestMap_OptionalColumns_AreLeftOutWhenTheAttributeIsAbsent(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(nil))

	for _, col := range []string{"next_hop", "src_asn", "dst_asn", "vlan_id", "sequence_num", "src_mac", "dst_mac", "icmp_type", "icmp_code"} {
		assert.NotContains(t, res.row, col)
	}
}

func TestMap_AsNumbers_FallBackToTheReceiverNames(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.src_as": int64(65000), "flow.dst_as": int64(65001)}))

	assert.Equal(t, int64(65000), res.row["src_asn"])
	assert.Equal(t, int64(65001), res.row["dst_asn"])
}

func TestMap_RouterHit_GivesTheSiteDataAndARecordDirectionWins(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.direction": "egress"}))

	assert.Equal(t, "site-a", res.row["account_id"])
	assert.Equal(t, "site-a", res.row["region"])
	assert.Equal(t, "egress", res.row["flow_direction"])
	assert.Empty(t, res.unknown)
}

func TestMap_UnlistedExporter_GivesEmptySiteDataAndRateOne(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{"flow.sampler_address": "192.0.2.99"}))

	assert.Equal(t, "", res.row["account_id"])
	assert.Equal(t, "", res.row["region"])
	assert.Equal(t, "", res.row["flow_direction"])
	assert.Equal(t, int32(1), res.row["sampling_rate"])
	assert.Equal(t, []string{"192.0.2.99"}, res.unknown)
}

func TestMap_RecordWithoutAnAddress_IsNotMapped(t *testing.T) {
	for _, key := range []string{"source.address", "destination.address"} {
		res := mapOne(t, testConfig(), flowAttrs(map[string]any{key: nil}))

		assert.False(t, res.ok, key)
	}
	assert.False(t, mapOne(t, testConfig(), flowAttrs(map[string]any{"source.address": "not an address"})).ok)
}

// ddlColumns reads the 60 columns of the DDL fixture.
func ddlColumns(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("testdata/flow_log_columns.txt")
	require.NoError(t, err)
	defer f.Close()
	var names []string
	for s := bufio.NewScanner(f); s.Scan(); {
		if name, _, ok := strings.Cut(s.Text(), " "); ok {
			names = append(names, name)
		}
	}
	return names
}

// tableDefaults are the columns the exporter never fills: they take the table default.
var tableDefaults = []string{
	"source_file", "insert_id", "loaded_at", "vpc_id", "peer_vpc_id", "subnet_id", "interface_id", "instance_id",
	"src_instance", "dst_instance", "pkt_srcaddr", "pkt_dstaddr", "type", "traffic_path", "log_status", "version",
	"src_k8s_cluster", "src_k8s_namespace", "src_k8s_pod", "src_k8s_workload", "dst_k8s_cluster", "dst_k8s_namespace",
	"dst_k8s_pod", "dst_k8s_workload", "src_service", "dst_service",
}

func TestColumns_EveryDDLColumnIsMappedOrLeftToTheTableDefault(t *testing.T) {
	ddl := ddlColumns(t)
	require.Len(t, ddl, 60)

	var table []string
	for _, c := range tableColumns {
		table = append(table, c.name)
	}
	assert.Equal(t, ddl, table, "the column list of the code is the DDL, in order")
	assert.ElementsMatch(t, ddl, append(append([]string(nil), mappedColumns...), tableDefaults...))
	assert.NotContains(t, mappedColumns, "loaded_at")
}

func TestColumns_ARecordWithEveryAttribute_FillsExactlyTheMappedColumns(t *testing.T) {
	res := mapOne(t, testConfig(), flowAttrs(map[string]any{
		"flow.next_hop": "198.51.100.1", "flow.vlan_id": int64(20), "flow.sequence_num": int64(77),
		"flow.src_mac": "00:11:22:33:44:55", "flow.dst_mac": "00:11:22:33:44:66", "flow.icmp_type": int64(8), "flow.icmp_code": int64(0),
		"source.as.number": int64(1), "destination.as.number": int64(2),
	}))

	var got []string
	for name := range res.row {
		got = append(got, name)
	}
	assert.ElementsMatch(t, mappedColumns, got)
}
