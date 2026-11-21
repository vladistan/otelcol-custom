// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

// kind is the ClickHouse type family of a column, as far as the native batch cares.
type kind int

const (
	kString kind = iota
	kInt32
	kInt64
	kUInt32
	kUInt8
	kDateTime
)

type column struct {
	name string
	kind kind
}

// tableColumns are the 60 columns of the flow table, in table order.
var tableColumns = []column{
	{"cloud", kString}, {"account_id", kString}, {"region", kString}, {"source_file", kString},
	{"insert_id", kString}, {"loaded_at", kDateTime}, {"loader", kString}, {"exporter", kString},
	{"collector", kString}, {"vpc_id", kString}, {"peer_vpc_id", kString}, {"subnet_id", kString},
	{"interface_id", kString}, {"input_interface", kString}, {"output_interface", kString},
	{"vlan_id", kInt32}, {"instance_id", kString}, {"src_instance", kString}, {"dst_instance", kString},
	{"srcaddr", kString}, {"dstaddr", kString}, {"pkt_srcaddr", kString}, {"pkt_dstaddr", kString},
	{"srcport", kInt32}, {"dstport", kInt32}, {"protocol", kInt32}, {"packets", kInt64}, {"bytes", kInt64},
	{"sampling_rate", kInt32}, {"tcp_flags", kInt32}, {"type", kString}, {"flow_direction", kString},
	{"traffic_path", kString}, {"dscp", kInt32}, {"next_hop", kString}, {"src_asn", kInt64}, {"dst_asn", kInt64},
	{"start_time", kDateTime}, {"end_time", kDateTime}, {"action", kString}, {"log_status", kString},
	{"version", kInt32}, {"src_k8s_cluster", kString}, {"src_k8s_namespace", kString}, {"src_k8s_pod", kString},
	{"src_k8s_workload", kString}, {"dst_k8s_cluster", kString}, {"dst_k8s_namespace", kString},
	{"dst_k8s_pod", kString}, {"dst_k8s_workload", kString}, {"src_service", kString}, {"dst_service", kString},
	{"flow_type", kString}, {"sequence_num", kUInt32}, {"received_at", kDateTime}, {"ip_version", kUInt8},
	{"src_mac", kString}, {"dst_mac", kString}, {"icmp_type", kUInt8}, {"icmp_code", kUInt8},
}

// mappedColumns are the columns the exporter can fill. Every other column takes the table default.
var mappedColumns = []string{
	"cloud", "account_id", "region", "loader", "exporter", "collector", "input_interface", "output_interface",
	"vlan_id", "srcaddr", "dstaddr", "srcport", "dstport", "protocol", "packets", "bytes", "sampling_rate",
	"tcp_flags", "flow_direction", "dscp", "next_hop", "src_asn", "dst_asn", "start_time", "end_time", "action",
	"flow_type", "sequence_num", "received_at", "ip_version", "src_mac", "dst_mac", "icmp_type", "icmp_code",
}

// zero returns the value a row without the column gets in a batch that fills it for other rows.
func (k kind) zero() any {
	switch k {
	case kInt32:
		return int32(0)
	case kInt64:
		return int64(0)
	case kUInt32:
		return uint32(0)
	case kUInt8:
		return uint8(0)
	case kDateTime:
		return zeroTime
	default:
		return ""
	}
}
