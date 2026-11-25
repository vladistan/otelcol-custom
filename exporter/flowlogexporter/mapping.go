// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"math"
	"net/netip"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

var zeroTime = time.Unix(0, 0).UTC()

// Reasons of the mapping warnings.
const (
	warnBadTime         = "bad_time"
	warnUnknownProtocol = "unknown_protocol"
)

// The calendar a flow time must fall in: 2000-01-01 up to 2100-01-01, in nanoseconds.
const (
	minTimeNs = 946684800 * int64(time.Second)
	maxTimeNs = 4102444800 * int64(time.Second)
)

// row holds the values of one record by column name; a column the record does not fill is absent.
type row map[string]any

// mapper turns a netflow log record into a row.
type mapper struct {
	loader, collector string
	routers           map[netip.Addr]Router
	warn              func(reason string)
	unknownExporter   func(addr string)
}

func newMapper(cfg *Config, collector string, warn func(string), unknown func(string)) *mapper {
	m := &mapper{loader: cfg.Loader, collector: collector, routers: map[netip.Addr]Router{}, warn: warn, unknownExporter: unknown}
	for _, r := range cfg.Routers {
		if addr, err := netip.ParseAddr(r.Address); err == nil {
			m.routers[addr.Unmap()] = r
		}
	}
	return m
}

// mapRecord returns the row of a record, or false when the record has no usable source or destination address.
func (m *mapper) mapRecord(rec plog.LogRecord) (row, bool) {
	a := rec.Attributes()
	src, okSrc := addrAttr(a, "source.address")
	dst, okDst := addrAttr(a, "destination.address")
	if !okSrc || !okDst {
		return nil, false
	}
	r := row{
		"cloud": "onprem", "loader": m.loader, "collector": m.collector,
		"srcaddr": src.String(), "dstaddr": dst.String(), "ip_version": ipVersion(src),
		"srcport": int32Attr(a, "source.port"), "dstport": int32Attr(a, "destination.port"),
		"protocol": m.protocol(a), "packets": int64Attr(a, "flow.io.packets"), "bytes": int64Attr(a, "flow.io.bytes"),
		"input_interface": strconv.FormatInt(int64Attr(a, "flow.in_if"), 10), "output_interface": strconv.FormatInt(int64Attr(a, "flow.out_if"), 10),
		"tcp_flags": int32Attr(a, "flow.tcp_flags"), "dscp": int32(int64Attr(a, "flow.ip_tos") >> 2),
		"action": action(a), "flow_type": strAttr(a, "flow.type"),
	}
	m.times(rec, r)
	m.site(a, r)
	optional(a, r)
	return r, true
}

// protocol maps the protocol name of the receiver, or a number written as text, to the IANA number.
func (m *mapper) protocol(a pcommon.Map) int32 {
	name := strAttr(a, "network.transport")
	if n, err := strconv.ParseInt(name, 10, 32); err == nil {
		return int32(n)
	}
	if n, ok := protocolNumbers[name]; ok {
		return n
	}
	m.warn(warnUnknownProtocol)
	return 0
}

// times sets start_time, end_time and received_at. A zero or out-of-range start or end gives the
// second of the receive time for both.
func (m *mapper) times(rec plog.LogRecord, r row) {
	a := rec.Attributes()
	recv := timeOr(int64Attr(a, "flow.time_received"), rec.ObservedTimestamp().AsTime())
	start, end := int64Attr(a, "flow.start"), int64Attr(a, "flow.end")
	startTime, endTime := second(time.Unix(0, start)), second(time.Unix(0, end))
	if !validNs(start) || !validNs(end) {
		m.warn(warnBadTime)
		startTime, endTime = second(recv), second(recv)
	}
	r["start_time"], r["end_time"], r["received_at"] = startTime, endTime, recv.UTC()
}

// site fills account, region, direction, exporter and sampling rate from the record and the router table.
func (m *mapper) site(a pcommon.Map, r row) {
	raw := strAttr(a, "flow.sampler_address")
	r["exporter"] = raw
	var route Router
	if addr, err := netip.ParseAddr(raw); err == nil {
		r["exporter"] = addr.String()
		if found, ok := m.routers[addr.Unmap()]; ok {
			route = found
		} else {
			m.unknownExporter(addr.String())
		}
	} else {
		m.unknownExporter(raw)
	}
	r["account_id"], r["region"] = route.AccountID, route.Region
	rate := int64Attr(a, "flow.sampling_rate")
	if rate <= 0 {
		rate = int64(route.SamplingRate)
	}
	if rate <= 0 {
		rate = 1
	}
	r["sampling_rate"] = clamp32(rate)
	r["flow_direction"] = route.Direction
	if dir := strAttr(a, "flow.direction"); dir != "" {
		r["flow_direction"] = dir
	}
}

// optional adds the columns whose source attribute may be missing; a missing one is left out of the row.
func optional(a pcommon.Map, r row) {
	if v := strAttr(a, "flow.next_hop"); v != "" {
		r["next_hop"] = v
	}
	if v, ok := firstInt(a, "source.as.number", "flow.src_as"); ok {
		r["src_asn"] = v
	}
	if v, ok := firstInt(a, "destination.as.number", "flow.dst_as"); ok {
		r["dst_asn"] = v
	}
	if v, ok := intAttr(a, "flow.vlan_id"); ok {
		r["vlan_id"] = clamp32(v)
	}
	if v, ok := intAttr(a, "flow.sequence_num"); ok {
		r["sequence_num"] = uint32(max(0, min(v, math.MaxUint32)))
	}
	for attr, col := range map[string]string{"flow.src_mac": "src_mac", "flow.dst_mac": "dst_mac"} {
		if v, ok := a.Get(attr); ok {
			r[col] = v.AsString()
		}
	}
	for attr, col := range map[string]string{"flow.icmp_type": "icmp_type", "flow.icmp_code": "icmp_code"} {
		if v, ok := intAttr(a, attr); ok {
			r[col] = uint8(max(0, min(v, math.MaxUint8)))
		}
	}
}

// action maps the forwarding status byte of the exporter (the top two bits, RFC 7270) to ACCEPT or REJECT.
func action(a pcommon.Map) string {
	code, ok := intAttr(a, "flow.forwarding_status")
	if !ok {
		return ""
	}
	switch code >> 6 {
	case 1:
		return "ACCEPT"
	case 2:
		return "REJECT"
	}
	return ""
}

func ipVersion(a netip.Addr) uint8 {
	if a.Unmap().Is4() {
		return 4
	}
	return 6
}

func validNs(ns int64) bool { return ns >= minTimeNs && ns < maxTimeNs }

func second(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func timeOr(ns int64, fallback time.Time) time.Time {
	if validNs(ns) {
		return time.Unix(0, ns)
	}
	if !fallback.IsZero() && fallback.Unix() > 0 {
		return fallback
	}
	return time.Now()
}

func clamp32(v int64) int32 { return int32(max(math.MinInt32, min(v, math.MaxInt32))) }

func addrAttr(a pcommon.Map, key string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strAttr(a, key))
	return addr, err == nil
}

func strAttr(a pcommon.Map, key string) string {
	if v, ok := a.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

// intAttr reads an integer attribute, or a number written as text.
func intAttr(a pcommon.Map, key string) (int64, bool) {
	v, ok := a.Get(key)
	if !ok {
		return 0, false
	}
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return v.Int(), true
	case pcommon.ValueTypeDouble:
		return int64(v.Double()), true
	case pcommon.ValueTypeStr:
		n, err := strconv.ParseInt(v.Str(), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func int64Attr(a pcommon.Map, key string) int64 {
	n, _ := intAttr(a, key)
	return n
}

func int32Attr(a pcommon.Map, key string) int32 { return clamp32(int64Attr(a, key)) }

func firstInt(a pcommon.Map, keys ...string) (int64, bool) {
	for _, k := range keys {
		if n, ok := intAttr(a, k); ok {
			return n, true
		}
	}
	return 0, false
}
