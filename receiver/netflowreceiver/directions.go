// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver"

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
)

// The NetFlow v9 field types of the IN and OUT byte and packet counters.
const (
	fieldInBytes  = netflow.NFV9_FIELD_IN_BYTES
	fieldInPkts   = netflow.NFV9_FIELD_IN_PKTS
	fieldOutBytes = netflow.NFV9_FIELD_OUT_BYTES
	fieldOutPkts  = netflow.NFV9_FIELD_OUT_PKTS
)

// foldDirections adds the OUT counter to the IN counter of every NetFlow v9 record and drops
// the OUT field, so the producer sees one counter. The goflow2 proto producer reads IN_BYTES and
// OUT_BYTES (and the packet pair) into the same field, so the one that comes last in the template
// wins. An exporter that lists the OUT counters after the IN counters and fills them with 0
// (OPNsense and FreeBSD ng_netflow do) would otherwise report 0 bytes and 0 packets for every flow.
//
// IPFIX is left alone: its types 23 and 24 are the counters after a middlebox, the same traffic
// as types 1 and 2, and adding them would count the traffic twice.
func foldDirections(msg any) {
	packet, ok := msg.(*netflow.NFv9Packet)
	if !ok {
		return
	}
	for _, set := range packet.FlowSets {
		data, ok := set.(netflow.DataFlowSet)
		if !ok {
			continue
		}
		for i := range data.Records {
			data.Records[i].Values = foldRecord(data.Records[i].Values)
		}
	}
}

// foldRecord adds the OUT counters of one record to its IN counters when the record has both.
// A record with only the OUT counter keeps it, as before.
func foldRecord(values []netflow.DataField) []netflow.DataField {
	values = foldPair(values, fieldInBytes, fieldOutBytes)
	return foldPair(values, fieldInPkts, fieldOutPkts)
}

// foldPair adds the counter of type out to the counter of type in and removes the out field.
// It changes nothing unless the record has both and each is a counter of up to 8 bytes; a sum
// past the 64-bit range stays at the maximum.
func foldPair(values []netflow.DataField, in, out uint16) []netflow.DataField {
	inAt, outAt := counterAt(values, in), counterAt(values, out)
	if inAt < 0 || outAt < 0 {
		return values
	}
	a, okA := counterValue(values[inAt])
	b, okB := counterValue(values[outAt])
	if !okA || !okB {
		return values
	}
	sum, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		sum = math.MaxUint64
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], sum)
	values[inAt].Value = buf[:]
	return append(values[:outAt], values[outAt+1:]...)
}

// counterAt is the index of the first plain (no enterprise number) field of the type, or -1.
func counterAt(values []netflow.DataField, typ uint16) int {
	for i := range values {
		if values[i].Type == typ && !values[i].PenProvided {
			return i
		}
	}
	return -1
}

// counterValue reads a big-endian counter of up to 8 bytes, the lengths the producer accepts.
func counterValue(f netflow.DataField) (uint64, bool) {
	b, ok := f.Value.([]byte)
	if !ok || len(b) == 0 || len(b) > 8 {
		return 0, false
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v, true
}
