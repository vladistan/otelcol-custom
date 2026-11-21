// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver"

import (
	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"
)

// The NetFlow v9 field types of the absolute flow times in milliseconds. They are the IPFIX numbers
// flowStartMilliseconds and flowEndMilliseconds, which Flexible NetFlow on IOS-XE uses in v9 too.
const (
	fieldFlowStartMilliseconds = 152
	fieldFlowEndMilliseconds   = 153
)

// The calendar a time in milliseconds must fall in: 2000-01-01 up to 2100-01-01.
const (
	minTimeMilliseconds = 946684800000
	maxTimeMilliseconds = 4102444800000
)

// fixV9Times sets the flow start and end of a NetFlow v9 message from the absolute millisecond
// fields when the record has them. goflow2 reads those fields for IPFIX only, and for v9 it reads
// the uptime-relative first and last switched times. A v9 record with no time field of either
// form gets start and end 0, not the export time of the header, so a reader can tell that the
// exporter sent no time. The messages come one per record, in the order of the data sets.
func fixV9Times(msg any, set []producer.ProducerMessage) {
	packet, ok := msg.(*netflow.NFv9Packet)
	if !ok {
		return
	}
	var records []netflow.DataRecord
	for _, fs := range packet.FlowSets {
		if data, ok := fs.(netflow.DataFlowSet); ok {
			records = append(records, data.Records...)
		}
	}
	if len(records) != len(set) {
		return
	}
	for i, m := range set {
		pm, ok := m.(*protoproducer.ProtoProducerMessage)
		if !ok {
			continue
		}
		values := records[i].Values
		start, hasStart := absoluteMilliseconds(values, fieldFlowStartMilliseconds)
		end, hasEnd := absoluteMilliseconds(values, fieldFlowEndMilliseconds)
		switch {
		case hasStart || hasEnd:
			if hasStart {
				pm.TimeFlowStartNs = start
			}
			if hasEnd {
				pm.TimeFlowEndNs = end
			}
		case counterAt(values, uint16(netflow.NFV9_FIELD_FIRST_SWITCHED)) < 0 && counterAt(values, uint16(netflow.NFV9_FIELD_LAST_SWITCHED)) < 0:
			pm.TimeFlowStartNs, pm.TimeFlowEndNs = 0, 0
		}
	}
}

// absoluteMilliseconds reads the field of the type as a time in milliseconds since the epoch and
// returns it in nanoseconds. present is true when the record has the field; a value outside the
// calendar gives 0, which the exporter counts as a bad time.
func absoluteMilliseconds(values []netflow.DataField, typ uint16) (ns uint64, present bool) {
	i := counterAt(values, typ)
	if i < 0 {
		return 0, false
	}
	ms, ok := counterValue(values[i])
	if !ok || ms < minTimeMilliseconds || ms >= maxTimeMilliseconds {
		return 0, true
	}
	return ms * 1_000_000, true
}
