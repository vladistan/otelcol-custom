// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"
)

// wlcLine is a controller line of the given facility, mnemonic and message.
func wlcLine(facility, mnemonic, content string) string {
	return "ap-01: *apfMsConnTask_0: Dec 16 00:00:44.695: %" + facility + "-5-" + mnemonic + ": apf_80211.c:13338 " + content
}

const (
	wlcClientAssociate = "Client Association: Client MAC: 00:00:5e:00:53:01, AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1."
	wlcAssocReqFailed  = "Failed to process association request: Client MAC: 00:00:5e:00:53:01, WLAN: 1, SSID: ExampleNet."
	wlcSuppRatesFailed = "Supported rates check failed. Mobile MAC: 00:00:5e:00:53:01"
)

func runWLC(t *testing.T, cfg *Config, line string) (body string, attrs map[string]any) {
	t.Helper()
	h := newHarness(t, cfg)
	lr, _ := h.run(ciscoInput(line, "wifi-ap.example.org", "<133>"+line))
	return lr.Body().Str(), lr.Attributes().AsRaw()
}

func TestCisco_ClientAssociate_BodyHasTheClientApWlanAndRadio(t *testing.T) {
	body, _ := runWLC(t, defaultConfig(), wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate))

	if want := "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 1 radio 2.4GHz"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestCisco_ClientAssociate_NoApNameDropsTheApPart(t *testing.T) {
	const content = "Client Association: Client MAC: 00:00:5e:00:53:01, Radio: 2.4GHz , WLAN Id: 1."
	body, attrs := runWLC(t, defaultConfig(), wlcLine("APF", "CLIENT_ASSOCIATE", content))

	if want := "Client Association 00:00:5e:00:53:01 wlan 1 radio 2.4GHz"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if _, ok := attrs["vstan.wifi.client.ap"]; ok {
		t.Errorf("vstan.wifi.client.ap = %v, want none", attrs["vstan.wifi.client.ap"])
	}
}

func TestCisco_FailureEvents_KeepTheirTextAndAddTheFacts(t *testing.T) {
	tests := []struct {
		name, mnemonic, content, body string
	}{
		{
			"association request failed", "ASSOCREQ_PROC_FAILED", wlcAssocReqFailed,
			"Failed to process association request 00:00:5e:00:53:01 wlan 1 ssid ExampleNet",
		},
		{
			"supported rates check failed", "CHECK_SUPP_RATES_FAILED", wlcSuppRatesFailed,
			"Supported rates check failed 00:00:5e:00:53:01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := runWLC(t, defaultConfig(), wlcLine("APF", tt.mnemonic, tt.content))
			if body != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestCisco_ClientMacAddress_EveryFormGivesTheSameValue(t *testing.T) {
	for _, mac := range []string{"00:00:5e:00:53:01", "00-00-5E-00-53-01", "0000.5e00.5301", "00:00:5E:00:53:01"} {
		content := "Client Association: Client MAC: " + mac + ", AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1."
		body, attrs := runWLC(t, defaultConfig(), wlcLine("APF", "CLIENT_ASSOCIATE", content))

		if attrs["client.mac.address"] != "00:00:5e:00:53:01" {
			t.Errorf("%s: client.mac.address = %v", mac, attrs["client.mac.address"])
		}
		if want := "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 1 radio 2.4GHz"; body != want {
			t.Errorf("%s: body = %q, want %q", mac, body, want)
		}
	}
}

func TestCisco_ClientMacAddress_IsOnEveryApfEventThatNamesAClient(t *testing.T) {
	for name, line := range map[string]string{
		"associate":           wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate),
		"association failed":  wlcLine("APF", "ASSOCREQ_PROC_FAILED", wlcAssocReqFailed),
		"supported rates":     wlcLine("APF", "CHECK_SUPP_RATES_FAILED", wlcSuppRatesFailed),
		"mac after from":      wlcLine("APF", "ASSOC_REJECT", "Association request from 0000.5e00.5301 rejected, WLAN Id: 3."),
		"client and no radio": wlcLine("APF", "CLIENT_DEAUTH", "Client Deauthentication: Client MAC: 00-00-5E-00-53-01"),
	} {
		_, attrs := runWLC(t, defaultConfig(), line)
		if attrs["client.mac.address"] != "00:00:5e:00:53:01" {
			t.Errorf("%s: client.mac.address = %v", name, attrs["client.mac.address"])
		}
	}
}

func TestCisco_ApOnlyEvent_HasNoClientMacAddress(t *testing.T) {
	const content = "Client Deauthentication: AP Name: ap-02, Radio: 5GHz , WLAN Id: 2."
	body, attrs := runWLC(t, defaultConfig(), wlcLine("APF", "CLIENT_DEAUTH", content))

	if _, ok := attrs["client.mac.address"]; ok {
		t.Errorf("client.mac.address = %v, want none", attrs["client.mac.address"])
	}
	if want := "Client Deauthentication ap ap-02 wlan 2 radio 5GHz"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestCisco_WifiAttributes_AreAsBeforeAndTheSSIDIsSetWhenKnown(t *testing.T) {
	_, attrs := runWLC(t, defaultConfig(), wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate))
	for k, want := range map[string]string{
		"vstan.wifi.client.ap": "ap-01", "vstan.wifi.radio": "2.4GHz", "vstan.wifi.wlan.id": "1",
		"vstan.wifi.ap.name": "ap-01", "vstan.cisco.facility": "APF", "vstan.cisco.mnemonic": "CLIENT_ASSOCIATE",
	} {
		if attrs[k] != want {
			t.Errorf("%s = %v, want %q", k, attrs[k], want)
		}
	}
	if _, ok := attrs["vstan.wifi.ssid"]; ok {
		t.Errorf("vstan.wifi.ssid = %v, want none: the line has no SSID", attrs["vstan.wifi.ssid"])
	}

	_, attrs = runWLC(t, defaultConfig(), wlcLine("APF", "ASSOCREQ_PROC_FAILED", wlcAssocReqFailed))
	if attrs["vstan.wifi.ssid"] != "ExampleNet" || attrs["vstan.wifi.wlan.id"] != "1" {
		t.Errorf("ssid = %v, wlan id = %v", attrs["vstan.wifi.ssid"], attrs["vstan.wifi.wlan.id"])
	}
}

func TestCisco_WLANSSIDs_FillAMissingSSIDFromTheWlanId(t *testing.T) {
	cfg := defaultConfig()
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}

	body, attrs := runWLC(t, cfg, wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate))

	if want := "Client Association 00:00:5e:00:53:01 ap ap-01 wlan 1 ssid MapNet radio 2.4GHz"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if attrs["vstan.wifi.ssid"] != "MapNet" {
		t.Errorf("vstan.wifi.ssid = %v", attrs["vstan.wifi.ssid"])
	}
}

func TestCisco_WLANSSIDs_TheSSIDOfTheMessageWins(t *testing.T) {
	cfg := defaultConfig()
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}

	body, attrs := runWLC(t, cfg, wlcLine("APF", "ASSOCREQ_PROC_FAILED", wlcAssocReqFailed))

	if attrs["vstan.wifi.ssid"] != "ExampleNet" || !strings.Contains(body, "ssid ExampleNet") || strings.Contains(body, "MapNet") {
		t.Errorf("body = %q, ssid = %v", body, attrs["vstan.wifi.ssid"])
	}
}

func TestCisco_WLANSSIDs_NoMapOrAnUnmappedIdLeavesTheSSIDOut(t *testing.T) {
	withOther := defaultConfig()
	withOther.WLANSSIDs = map[string]string{"7": "OtherNet"}
	for name, cfg := range map[string]*Config{"no map": defaultConfig(), "id not in the map": withOther} {
		body, attrs := runWLC(t, cfg, wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate))
		if _, ok := attrs["vstan.wifi.ssid"]; ok || strings.Contains(body, "ssid") {
			t.Errorf("%s: body = %q, ssid = %v, want no SSID", name, body, attrs["vstan.wifi.ssid"])
		}
	}
}

func TestCisco_RewriteOff_KeepsTheBodyButSetsTheFactsAsAttributes(t *testing.T) {
	cfg := defaultConfig()
	cfg.Cisco.RewriteMessages = false
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}
	line := wlcLine("APF", "CLIENT_ASSOCIATE", wlcClientAssociate)

	h := newHarness(t, cfg)
	lr, _ := h.run(ciscoInput(line, "wifi-ap.example.org", "<133>ap-01: message"))

	if got := lr.Body().Str(); got != "ap-01: message" {
		t.Errorf("body = %q, want the body apart from the priority prefix", got)
	}
	a := lr.Attributes().AsRaw()
	if a["client.mac.address"] != "00:00:5e:00:53:01" || a["vstan.wifi.ssid"] != "MapNet" {
		t.Errorf("attributes = %v", a)
	}
}

func TestCisco_OtherWirelessFacilities_KeepTheirEarlierOutput(t *testing.T) {
	const content = "Some dot1x event: Client MAC: 00:00:5e:00:53:01, AP Name: ap-01, Radio: 2.4GHz , WLAN Id: 1."
	cfg := defaultConfig()
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}

	body, attrs := runWLC(t, cfg, wlcLine("DOT1X", "EVENT", content))

	if body != "Some dot1x event" {
		t.Errorf("body = %q, want the message without the extracted fields", body)
	}
	for _, k := range []string{"client.mac.address", "vstan.wifi.ssid"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("%s = %v, want none for a facility other than APF", k, attrs[k])
		}
	}
	if attrs["vstan.wifi.client.ap"] != "ap-01" || attrs["vstan.wifi.wlan.id"] != "1" || attrs["vstan.wifi.radio"] != "2.4GHz" {
		t.Errorf("attributes = %v", attrs)
	}
}

func TestCisco_NonWirelessLines_GiveTheirEarlierOutput(t *testing.T) {
	cfg := defaultConfig()
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}
	tests := []struct {
		name, line, body string
	}{
		{"IOS line", "661: *Dec 16 00:36:36.024: %SYS-5-CONFIG_I: Configured from console by vty0", "Configured from console by vty0"},
		{"switch line", "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to down", "Interface GigabitEthernet0/1, changed state to down"},
		{
			"HP switch line", " Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: traps.c(763) 284 %% Spanning Tree Topology Change Received",
			"Spanning Tree Topology Change Received",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, cfg)
			lr, _ := h.run(ciscoInput(tt.line, "", tt.line))
			if lr.Body().Str() != tt.body {
				t.Errorf("body = %q, want %q", lr.Body().Str(), tt.body)
			}
			for _, k := range []string{"client.mac.address", "vstan.wifi.ssid"} {
				absent(t, lr.Attributes(), k)
			}
		})
	}
}

func TestCisco_AccessPointKernelLine_GivesItsEarlierOutput(t *testing.T) {
	h := newHarness(t, defaultConfig())
	lr, _ := h.run(input{body: "[*11/20/2026 06:11:26.3538] CAPWAP State: Run"})

	if lr.Body().Str() != "CAPWAP State: Run" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	absent(t, lr.Attributes(), "client.mac.address", "vstan.wifi.ssid")
}

func TestCisco_Wireless_SecondPassChangesNothing(t *testing.T) {
	cfg := defaultConfig()
	cfg.WLANSSIDs = map[string]string{"1": "MapNet"}
	var recs []auditRec
	for _, content := range []string{wlcClientAssociate, wlcAssocReqFailed, wlcSuppRatesFailed} {
		line := wlcLine("APF", "CLIENT_ASSOCIATE", content)
		recs = append(recs,
			auditRec{body: "<133>" + line, attrs: map[string]any{"log.record.original": line}},
			auditRec{body: line, attrs: map[string]any{}},
		)
	}

	assertTwoPassesEqual(t, cfg, map[string]any{}, recs)
}

func TestConfigValidate_WLANSSIDs(t *testing.T) {
	tests := []struct {
		name string
		ssid map[string]string
		want string
	}{
		{"valid", map[string]string{"1": "ExampleNet", "12": "Guest Net"}, ""},
		{"empty id", map[string]string{"": "ExampleNet"}, "wlan_ssids: empty WLAN id"},
		{"empty ssid", map[string]string{"1": ""}, `wlan_ssids["1"]: empty SSID`},
		{"id that is not a number", map[string]string{"guest": "ExampleNet"}, `wlan_ssids["guest"]: the WLAN id must be a number`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WLANSSIDs = tt.ssid
			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("Validate: %v", err)
			case tt.want != "" && (err == nil || err.Error() != tt.want):
				t.Fatalf("Validate = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDocs_ShowTheWirelessBodyAndWLANSSIDs(t *testing.T) {
	doc := readDoc(t)
	for _, want := range []string{
		"Client Association 00:00:5e:00:53:01 ap ap-01 wlan 1 radio 2.4GHz",
		"client.mac.address",
		"vstan.wifi.ssid",
		"| `wlan_ssids` |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the docs lack %q", want)
		}
	}
}
