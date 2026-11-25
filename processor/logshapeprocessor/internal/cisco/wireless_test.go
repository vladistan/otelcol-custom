// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cisco

import "testing"

func TestNormalizeMAC(t *testing.T) {
	const want = "00:00:5e:00:53:01"
	for _, in := range []string{"00:00:5e:00:53:01", "00:00:5E:00:53:01", "00-00-5E-00-53-01", "0000.5e00.5301", "0000.5E00.5301"} {
		got, ok := NormalizeMAC(in)
		if !ok || got != want {
			t.Errorf("NormalizeMAC(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "00:00:5e:00:53", "00:00:5e:00:53:01:02", "zz:00:5e:00:53:01", "00:00-5e:00:53:01", "0000.5e00"} {
		if got, ok := NormalizeMAC(in); ok {
			t.Errorf("NormalizeMAC(%q) = %q, want no match", in, got)
		}
	}
}

func TestParseWireless(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		mac       string
		ap        string
		wlan      string
		ssid      string
		radio     string
		text      string
		macInText bool
		body      string
	}{
		{
			name:    "client_associate",
			content: "Client Association: Client MAC: 00:00:5e:00:53:01, AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1.",
			mac:     "00:00:5e:00:53:01", ap: "ap-01", wlan: "1", radio: "2.4GHz",
			text: "Client Association",
			body: "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 1 radio 2.4GHz",
		},
		{
			name:    "client_associate_without_an_ap_name",
			content: "Client Association: Client MAC: 00:00:5e:00:53:01, Radio: 2.4GHz , WLAN Id: 1.",
			mac:     "00:00:5e:00:53:01", wlan: "1", radio: "2.4GHz",
			text: "Client Association",
			body: "Client Association 00:00:5e:00:53:01 wlan 1 radio 2.4GHz",
		},
		{
			name:    "association_request_failed_with_an_ssid",
			content: "Failed to process association request: Client MAC: 00:00:5e:00:53:01, WLAN: 1, SSID: ExampleNet.",
			mac:     "00:00:5e:00:53:01", wlan: "1", ssid: "ExampleNet",
			text: "Failed to process association request",
			body: "Failed to process association request 00:00:5e:00:53:01 wlan 1 ssid ExampleNet",
		},
		{
			name:    "ssid_with_a_space",
			content: "Failed to process association request: Client MAC: 00:00:5e:00:53:01, WLAN: 1, SSID: Example Net, Radio: 5GHz .",
			mac:     "00:00:5e:00:53:01", wlan: "1", ssid: "Example Net", radio: "5GHz",
			text: "Failed to process association request",
			body: "Failed to process association request 00:00:5e:00:53:01 wlan 1 ssid Example Net radio 5GHz",
		},
		{
			name:    "mobile_mac_label",
			content: "Supported rates check failed. Mobile MAC: 00:00:5e:00:53:01",
			mac:     "00:00:5e:00:53:01",
			text:    "Supported rates check failed",
			body:    "Supported rates check failed 00:00:5e:00:53:01",
		},
		{
			name:    "hyphen_form",
			content: "Client Association: Client MAC: 00-00-5E-00-53-01, AP Name: ap-01, Radio: 5GHz , WLAN Id: 2.",
			mac:     "00:00:5e:00:53:01", ap: "ap-01", wlan: "2", radio: "5GHz",
			text: "Client Association",
			body: "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 2 radio 5GHz",
		},
		{
			name:    "dotted_form",
			content: "Client Association: Client MAC: 0000.5e00.5301, AP Name: ap-01, Radio: 5GHz , WLAN Id: 2.",
			mac:     "00:00:5e:00:53:01", ap: "ap-01", wlan: "2", radio: "5GHz",
			text: "Client Association",
			body: "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 2 radio 5GHz",
		},
		{
			name:    "mac_after_from_stays_in_the_text",
			content: "Association request from 0000.5e00.5301 rejected, WLAN Id: 3.",
			mac:     "00:00:5e:00:53:01", wlan: "3",
			text:      "Association request from 00:00:5e:00:53:01 rejected",
			macInText: true,
			body:      "Association request from 00:00:5e:00:53:01 rejected wlan 3",
		},
		{
			name:    "ap_only_event_has_no_client",
			content: "Client Deauthentication: AP Name: ap-02, Radio: 5GHz , WLAN Id: 2.",
			ap:      "ap-02", wlan: "2", radio: "5GHz",
			text: "Client Deauthentication",
			body: "Client Deauthentication ap ap-02 wlan 2 radio 5GHz",
		},
		{
			name:    "ap_name_last_with_a_full_stop",
			content: "Client Association: Client MAC: 00:00:5e:00:53:01, Radio: 5GHz , WLAN Id: 2, AP Name: ap-03.",
			mac:     "00:00:5e:00:53:01", ap: "ap-03", wlan: "2", radio: "5GHz",
			text: "Client Association",
			body: "Client Association 00:00:5e:00:53:01 ap ap-03 wlan 2 radio 5GHz",
		},
		{
			name:    "no_facts",
			content: "Some event happened",
			text:    "Some event happened",
			body:    "Some event happened",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := ParseWireless(tt.content)
			if w.ClientMAC != tt.mac || w.APName != tt.ap || w.WLANID != tt.wlan || w.SSID != tt.ssid || w.Radio != tt.radio {
				t.Errorf("facts = %+v, want mac %q ap %q wlan %q ssid %q radio %q", w, tt.mac, tt.ap, tt.wlan, tt.ssid, tt.radio)
			}
			if w.Text != tt.text || w.MACInText != tt.macInText {
				t.Errorf("text = %q (in text %v), want %q (%v)", w.Text, w.MACInText, tt.text, tt.macInText)
			}
			if got := w.Body(w.SSID); got != tt.body {
				t.Errorf("body = %q, want %q", got, tt.body)
			}
		})
	}
}

func TestWireless_BodyTakesTheSSIDItIsGiven(t *testing.T) {
	w := ParseWireless("Failed to process association request: Client MAC: 00:00:5e:00:53:01, WLAN: 1.")
	if got, want := w.Body("ExampleNet"), "Failed to process association request 00:00:5e:00:53:01 wlan 1 ssid ExampleNet"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got, want := w.Body(""), "Failed to process association request 00:00:5e:00:53:01 wlan 1"; got != want {
		t.Errorf("body without an ssid = %q, want %q", got, want)
	}
}

func TestParseWLCLog_KeepsTheContentForTheWirelessFacts(t *testing.T) {
	in := "ap-01: *apfMsConnTask_0: Dec 16 00:00:44.695: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: 00:00:5e:00:53:01, AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1."
	info := ParseWLCLog(in)
	if info == nil {
		t.Fatal("not a WLC line")
	}
	const want = "Client Association: Client MAC: 00:00:5e:00:53:01, AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1."
	if info.Content != want {
		t.Errorf("content = %q, want %q", info.Content, want)
	}
}
