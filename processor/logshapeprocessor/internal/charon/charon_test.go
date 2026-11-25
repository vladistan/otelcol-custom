// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore ipsec charon strongswan

package charon

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		thread    string
		component string
		connID    string
		connSeq   string
		message   string
		extra     map[string]string
	}{
		{"no connection part", "07[IKE] sending DPD request", "07", "IKE", "", "", "sending DPD request", map[string]string{}},
		{"connection with a sequence", "13[IKE] <vpn-client|82> CHILD_SA vpn-client{3} closed", "13", "IKE", "vpn-client", "82", "CHILD_SA vpn-client{3} closed",
			map[string]string{"ipsec.child_sa": "vpn-client", "ipsec.child_sa_num": "3", "ipsec.child_sa_state": "closed"}},
		{"connection that is only a number", "14[CFG] <12> received stroke: add connection", "14", "CFG", "12", "", "received stroke: add connection", map[string]string{}},
		{"packet line", "14[NET] sending packet: from 192.0.2.1[500] to 203.0.113.50[500] (1164 bytes)", "14", "NET", "", "", "sending packet 1164 bytes 203.0.113.50",
			map[string]string{
				"ipsec.direction": "sending", "ipsec.local_ip": "192.0.2.1", "ipsec.local_port": "500",
				"ipsec.remote_ip": "203.0.113.50", "ipsec.remote_port": "500", "ipsec.packet_size": "1164",
			}},
		{"received packet", "14[NET] received packet: from 203.0.113.50[4500] to 192.0.2.1[4500] (80 bytes)", "14", "NET", "", "", "received packet 80 bytes 203.0.113.50",
			map[string]string{
				"ipsec.direction": "received", "ipsec.local_ip": "192.0.2.1", "ipsec.local_port": "4500",
				"ipsec.remote_ip": "203.0.113.50", "ipsec.remote_port": "4500", "ipsec.packet_size": "80",
			}},
		{"authentication", "09[IKE] authentication of 'gw.example.org' with pre-shared key successful", "09", "IKE", "", "", "authentication of 'gw.example.org' with pre-shared key successful",
			map[string]string{"ipsec.auth_identity": "gw.example.org", "ipsec.auth_method": "pre-shared key", "ipsec.auth_result": "successful"}},
		{"selected proposal", "10[CFG] <vpn-client|2> selected proposal: ESP:AES_GCM_16_256/NO_EXT_SEQ", "10", "CFG", "vpn-client", "2", "selected proposal: ESP:AES_GCM_16_256/NO_EXT_SEQ",
			map[string]string{"ipsec.proposal": "ESP:AES_GCM_16_256/NO_EXT_SEQ"}},
		{"thread that is not digits", "ab[IKE] sending DPD request", "ab", "IKE", "", "", "sending DPD request", map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.in)
			if !ok {
				t.Fatalf("Parse(%q) found no charon line", tt.in)
			}
			if got.Thread != tt.thread || got.Component != tt.component || got.ConnID != tt.connID || got.ConnSeq != tt.connSeq || got.Message != tt.message {
				t.Errorf("Parse(%q) = thread %q component %q conn %q|%q message %q; want %q %q %q|%q %q",
					tt.in, got.Thread, got.Component, got.ConnID, got.ConnSeq, got.Message, tt.thread, tt.component, tt.connID, tt.connSeq, tt.message)
			}
			if !reflect.DeepEqual(got.Extra, tt.extra) {
				t.Errorf("extra = %v, want %v", got.Extra, tt.extra)
			}
		})
	}
}

func TestParse_NotACharonLine(t *testing.T) {
	for _, in := range []string{
		"", "plain text", "[IKE] no thread", "07[ike] lower case component", "07[IKE]", "07[IKE]   ",
		"Starting strongSwan 5.9.8 IPsec [starter]...", "07[TOOLONGNAME] text", "07[I] text",
	} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) took the line for a charon line", in)
		}
	}
}

func TestThreadID(t *testing.T) {
	for in, want := range map[string]struct {
		n  int64
		ok bool
	}{"07": {7, true}, "00": {0, true}, "13": {13, true}, "123": {123, true}, "ab": {0, false}, "1a": {0, false}, "": {0, false}} {
		if n, ok := ThreadID(in); n != want.n || ok != want.ok {
			t.Errorf("ThreadID(%q) = %d, %v; want %d, %v", in, n, ok, want.n, want.ok)
		}
	}
}

func TestLevel(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"received AUTH_FAILED notify error", "error"},
		{"authentication of 'gw.example.org' with RSA signature failed", "error"},
		{"signature validation failed, looking for another key", "error"},
		{"no shared key found for 'a' - 'b'", "error"},
		{"no private key found for 'gw.example.org'", "error"},
		{"no trusted RSA public key found for 'client.example.org'", "error"},
		{"no IKE config found for 192.0.2.1...203.0.113.50, sending NO_PROPOSAL_CHOSEN", "error"},
		{"giving up after 5 retransmits", "error"},
		{"failed to establish CHILD_SA, keeping IKE_SA", "error"},
		{"peer didn't accept the proposal", "warn"},
		{"received NO_PROPOSAL_CHOSEN notify, no CHILD_SA built", "warn"},
		{"retransmit 3 of request with message ID 0", "warn"},
		{"unable to resolve %any, initiate aborted", "warn"},
		{"received TS_UNACCEPTABLE notify, no CHILD_SA built", "warn"},
		{"received INVALID_KE_PAYLOAD notify", "warn"},
		{"peer not responding, trying again", "warn"},
		{"error writing to socket: Network is unreachable", "warn"},
		{"sending DPD request", ""},
		{"received DPD request, sending DPD response", ""},
		{"IKE_SA vpn-client[1] established between 192.0.2.1[gw.example.org]...203.0.113.50[client.example.org]", ""},
		{"CHILD_SA tunnel{1} established with SPIs c12345ab_i d67890cd_o and TS 10.0.0.0/24 === 10.1.0.0/24", ""},
		{"authentication of 'gw.example.org' with pre-shared key successful", ""},
		{"deleting IKE_SA vpn-client[1] between 192.0.2.1[gw.example.org]...203.0.113.50[client.example.org]", ""},
		{"IKE_SA deleted", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Level(tt.msg); got != tt.want {
			t.Errorf("Level(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}
