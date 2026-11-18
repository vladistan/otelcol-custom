// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !solaris

package netflowreceiver

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests use field and be32, which byte_counters_test.go defines for the same package.

func be64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func valueOf(t *testing.T, values []netflow.DataField, typ uint16) uint64 {
	t.Helper()
	for _, f := range values {
		if f.Type == typ {
			v, ok := counterValue(f)
			require.True(t, ok)
			return v
		}
	}
	t.Fatalf("no field of type %d", typ)
	return 0
}

func has(values []netflow.DataField, typ uint16) bool {
	for _, f := range values {
		if f.Type == typ {
			return true
		}
	}
	return false
}

func TestFoldRecordAddsAndDropsTheOutFields(t *testing.T) {
	in := []netflow.DataField{
		field(fieldInPkts, be32(3)), field(fieldInBytes, be32(100)),
		field(fieldOutPkts, be32(2)), field(fieldOutBytes, be32(50)),
		field(4, []byte{6}), // protocol, must stay
	}

	got := foldRecord(in)

	assert.Len(t, got, 3)
	assert.Equal(t, uint64(150), valueOf(t, got, fieldInBytes))
	assert.Equal(t, uint64(5), valueOf(t, got, fieldInPkts))
	assert.False(t, has(got, fieldOutBytes))
	assert.False(t, has(got, fieldOutPkts))
	assert.True(t, has(got, 4))
}

func TestFoldRecordAddsCountersOfDifferentLengths(t *testing.T) {
	got := foldRecord([]netflow.DataField{
		field(fieldInBytes, []byte{0x01, 0x00}), // 256 in 2 bytes
		field(fieldOutBytes, be64(10)),          // 10 in 8 bytes
	})

	assert.Equal(t, uint64(266), valueOf(t, got, fieldInBytes))
}

func TestFoldRecordStopsAtTheLargestNumber(t *testing.T) {
	got := foldRecord([]netflow.DataField{
		field(fieldInBytes, be64(math.MaxUint64)),
		field(fieldOutBytes, be64(5)),
	})

	assert.Equal(t, uint64(math.MaxUint64), valueOf(t, got, fieldInBytes))
}

func TestFoldRecordLeavesAPairAloneWhenOneSideIsAbsent(t *testing.T) {
	onlyIn := []netflow.DataField{field(fieldInBytes, be32(9))}
	onlyOut := []netflow.DataField{field(fieldOutBytes, be32(9))}

	assert.Equal(t, onlyIn, foldRecord(onlyIn))
	assert.Equal(t, onlyOut, foldRecord(onlyOut))
}

func TestFoldRecordSkipsEnterpriseFieldsWithTheSameNumber(t *testing.T) {
	vendor := netflow.DataField{PenProvided: true, Pen: 9, Type: fieldOutBytes, Value: be32(7)}
	in := []netflow.DataField{field(fieldInBytes, be32(100)), vendor}

	got := foldRecord(in)

	assert.Equal(t, []netflow.DataField{field(fieldInBytes, be32(100)), vendor}, got)
}

func TestFoldRecordSkipsValuesItCannotRead(t *testing.T) {
	for name, out := range map[string]netflow.DataField{
		"not bytes": {Type: fieldOutBytes, Value: "7"},
		"empty":     field(fieldOutBytes, nil),
		"too long":  field(fieldOutBytes, make([]byte, 9)),
	} {
		t.Run(name, func(t *testing.T) {
			in := []netflow.DataField{field(fieldInBytes, be32(100)), out}

			got := foldRecord(in)

			assert.Equal(t, []netflow.DataField{field(fieldInBytes, be32(100)), out}, got)
		})
	}
}

func TestFoldDirectionsIgnoresOtherMessages(t *testing.T) {
	assert.NotPanics(t, func() {
		foldDirections(nil)
		foldDirections("not a packet")
		foldDirections(&netflow.NFv9Packet{FlowSets: []any{"x", netflow.TemplateFlowSet{}}})
		foldDirections(&netflow.NFv9Packet{FlowSets: []any{netflow.DataFlowSet{}}})
	})
}

func TestFoldDirectionsChangesEveryRecordOfEveryDataSet(t *testing.T) {
	record := func() netflow.DataRecord {
		return netflow.DataRecord{Values: []netflow.DataField{
			field(fieldInBytes, be32(1)), field(fieldOutBytes, be32(2)),
		}}
	}
	packet := &netflow.NFv9Packet{FlowSets: []any{
		netflow.DataFlowSet{Records: []netflow.DataRecord{record(), record()}},
		netflow.DataFlowSet{Records: []netflow.DataRecord{record()}},
	}}

	foldDirections(packet)

	for _, set := range packet.FlowSets {
		for _, r := range set.(netflow.DataFlowSet).Records {
			assert.Len(t, r.Values, 1)
			assert.Equal(t, uint64(3), valueOf(t, r.Values, fieldInBytes))
		}
	}
}
