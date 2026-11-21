// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package openvpnstatusreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestPollLogsCarryTheClientPortAsAnInteger(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	recs := h.records()
	require.Len(t, recs, 2)
	port, ok := recs[0].Attributes().Get("client.port")
	require.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeInt, port.Type())
	assert.Equal(t, int64(51234), port.Int())
	port, ok = recs[1].Attributes().Get("client.port")
	require.True(t, ok)
	assert.Equal(t, int64(40222), port.Int())
}

func TestPollLogsLeaveTheOldNamesOut(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	attrs := h.records()[0].Attributes()
	for _, old := range []string{"common_name", "real_address", "bytes_received", "bytes_sent", "connected_since"} {
		_, ok := attrs.Get(old)
		assert.False(t, ok, "%s is still written", old)
	}
}

func TestPollUndefClientIsMarkedUnauthenticatedInTheLog(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-undef.txt")

	h.poll()

	attrs := h.records()[0].Attributes()
	flag, ok := attrs.Get("unauthenticated")
	require.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeBool, flag.Type())
	assert.True(t, flag.Bool())
	assert.Equal(t, "UNDEF", attrStr(t, h.records()[0], "user.name"))
}

func TestPollAuthenticatedClientHasNoUnauthenticatedFlag(t *testing.T) {
	h := newHarness(t, false)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	for _, rec := range h.records() {
		_, ok := rec.Attributes().Get("unauthenticated")
		assert.False(t, ok)
	}
}

func TestPollMetricsUseTheNewNamesAndMarkUndef(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-undef.txt")

	h.poll()

	dps := h.metric(t, bytesReceivedMetric).Sum().DataPoints()
	require.Equal(t, 1, dps.Len())
	attrs := dps.At(0).Attributes()
	name, _ := attrs.Get("user.name")
	addr, _ := attrs.Get("client.address")
	assert.Equal(t, "UNDEF", name.Str())
	assert.Equal(t, "192.0.2.44", addr.Str())
	flag, ok := attrs.Get("unauthenticated")
	require.True(t, ok)
	assert.True(t, flag.Bool())
	for _, old := range []string{"common_name", "real_address"} {
		_, found := attrs.Get(old)
		assert.False(t, found, "%s is still written", old)
	}
}

func TestPollMetricsOfAKnownClientHaveNoUnauthenticatedFlag(t *testing.T) {
	h := newMetricsHarness(t)
	h.writeFixture(t, "v1-two-clients.txt")

	h.poll()

	dps := h.metric(t, bytesSentMetric).Sum().DataPoints()
	for i := range dps.Len() {
		_, ok := dps.At(i).Attributes().Get("unauthenticated")
		assert.False(t, ok)
	}
}
