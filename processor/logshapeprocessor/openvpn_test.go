// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import "testing"

// The attributes of an openvpnstatus record, which has an empty body.
func openvpnRecord() map[string]any {
	return map[string]any{
		"common_name": "tyler", "real_address": "74.70.174.118:51234",
		"bytes_received": int64(32715571), "bytes_sent": int64(46766490), "connected_since": "2026-11-18 18:58:58",
	}
}

func TestOpenVPN_EmptyBodyRecord_GetsTheOneLineBodyAndTheOfficialNames(t *testing.T) {
	lr, _ := runW4(t, input{attrs: openvpnRecord()})

	w4Body(t, lr, "tyler connected since 2026-11-18 18:58:58 from 74.70.174.118, in 31.2MiB, out 44.6MiB")
	a := lr.Attributes()
	if attrStr(t, a, "user.name") != "tyler" || attrStr(t, a, "client.address") != "74.70.174.118" || attrStr(t, a, "vstan.openvpn.connected_since") != "2026-11-18 18:58:58" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "client.port", 51234)
	attrInt(t, a, "vstan.openvpn.bytes_received", 32715571)
	attrInt(t, a, "vstan.openvpn.bytes_sent", 46766490)
	absent(t, a, "common_name", "real_address", "bytes_received", "bytes_sent", "connected_since")
}

func TestOpenVPN_RecordWithTheOfficialNamesAlready_GetsTheSameBody(t *testing.T) {
	lr, _ := runW4(t, input{attrs: map[string]any{
		"user.name": "tyler", "client.address": "74.70.174.118", "client.port": int64(51234),
		"bytes_received": int64(32715571), "bytes_sent": int64(46766490), "connected_since": "2026-11-18 18:58:58",
	}})

	w4Body(t, lr, "tyler connected since 2026-11-18 18:58:58 from 74.70.174.118, in 31.2MiB, out 44.6MiB")
}

// The names the openvpnstatus receiver writes today.
func TestOpenVPN_RecordFromTheReceiver_GetsTheBodyAndKeepsItsNames(t *testing.T) {
	lr, _ := runW4(t, input{attrs: map[string]any{
		"user.name": "tyler", "client.address": "74.70.174.118", "client.port": int64(51234),
		"vstan.openvpn.bytes_received": int64(32715571), "vstan.openvpn.bytes_sent": int64(46766490),
		"vstan.openvpn.connected_since": "2026-11-18 18:58:58",
	}})

	w4Body(t, lr, "tyler connected since 2026-11-18 18:58:58 from 74.70.174.118, in 31.2MiB, out 44.6MiB")
	a := lr.Attributes()
	attrInt(t, a, "client.port", 51234)
	attrInt(t, a, "vstan.openvpn.bytes_received", 32715571)
	if attrStr(t, a, "vstan.openvpn.connected_since") != "2026-11-18 18:58:58" || a.Len() != 6 {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
}

func TestOpenVPN_UnrelatedKeysOfTheRecord_AreNotRenamed(t *testing.T) {
	attrs := openvpnRecord()
	attrs["hostname"] = "vpn1"
	attrs["pid"] = int64(42)

	lr, _ := runW4(t, input{attrs: attrs})

	a := lr.Attributes()
	if attrStr(t, a, "hostname") != "vpn1" {
		t.Fatalf("an unrelated key was renamed: %v", a.AsRaw())
	}
	attrInt(t, a, "pid", 42)
}

func TestOpenVPN_RecordWithABody_IsNotClaimed(t *testing.T) {
	attrs := openvpnRecord()

	lr, _ := runW4(t, input{body: "already has a body", attrs: attrs})

	if lr.Body().Str() != "already has a body" || lr.Attributes().Len() != len(attrs) {
		t.Fatalf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
}

func TestOpenVPN_RecordWithoutTheCounters_IsNotClaimed(t *testing.T) {
	lr, _ := runW4(t, input{attrs: map[string]any{"common_name": "tyler"}})

	if lr.Body().Str() != "" || lr.Attributes().Len() != 1 {
		t.Fatalf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
}
