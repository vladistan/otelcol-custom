// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore lldpd lldp vstan descr

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// REAL record of an lldpd neighbour, as the file receiver stored it: the body is the raw JSON line.
// Only the MAC, the switch name and the address are replaced. The branch is lldpd.
const lldpdNeighbour = `{"observed_at":"2026-11-20T13:06:13Z","interface":"eno2","via":"LLDP","age":"4 days, 17:24:51","chassis_id":"00:11:22:33:44:55","chassis_name":"switch-48.example.net","chassis_descr":"Cisco IOS Software, Catalyst L3 Switch Software (CAT3K_CAA-UNIVERSALK9-M), Version 15.0(1)EX3, RELEASE SOFTWARE (fc2)\nTechnical Support: http://www.cisco.com/techsupport\nCopyright (c) 1986-2013 by Cisco Systems, Inc.\nCompiled Mon 23-Sep-13 18:24 by prod_r","mgmt_ip":["192.0.2.39"],"port_id":"Gi4/0/18","port_descr":"GigabitEthernet4/0/18"}`

// lldpdAttrs are the attributes the file receiver copied from the JSON fields.
func lldpdAttrs() map[string]any {
	return map[string]any{
		"log.source": "lldp", "interface": "eno2", "client.mac": "00:11:22:33:44:55", "client.ip": "192.0.2.39",
		"chassis_id": "00:11:22:33:44:55", "chassis_name": "switch-48.example.net", "port_id": "Gi4/0/18",
		"port_descr": "GigabitEthernet4/0/18", "via": "LLDP", "age": "4 days, 17:24:51", "observed_at": "2026-11-20T13:06:13Z",
		"mgmt_ip": `["192.0.2.39"]`,
	}
}

func TestLldpd_NeighbourRecord_GetsTheOneLineBodyTheTimeAndTheOfficialNames(t *testing.T) {
	lr, _ := runW4(t, input{body: lldpdNeighbour, attrs: lldpdAttrs()})

	w4Body(t, lr, "eno2 sees switch-48.example.net port Gi4/0/18 192.0.2.39")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	w4Time(t, lr, time.Date(2026, 11, 20, 13, 6, 13, 0, time.UTC))
	a := lr.Attributes()
	for k, want := range map[string]string{
		"network.interface.name": "eno2", "client.mac.address": "00:11:22:33:44:55", "client.address": "192.0.2.39",
		"vstan.lldp.chassis_id": "00:11:22:33:44:55", "vstan.lldp.chassis_name": "switch-48.example.net",
		"vstan.lldp.port_id": "Gi4/0/18", "vstan.lldp.port_descr": "GigabitEthernet4/0/18",
		"vstan.lldp.via": "LLDP", "vstan.lldp.age": "4 days, 17:24:51", "log.record.original": lldpdNeighbour, "log.source": "lldp",
	} {
		if got := attrStr(t, a, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	absent(t, a, "interface", "client.mac", "client.ip", "chassis_id", "chassis_name", "port_id", "port_descr", "via", "age", "observed_at", "mgmt_ip")
}

func TestLldpd_MultiLineChassisDescription_StaysAnAttributeNotTheBody(t *testing.T) {
	lr, _ := runW4(t, input{body: lldpdNeighbour, attrs: lldpdAttrs()})

	descr := attrStr(t, lr.Attributes(), "vstan.lldp.chassis_descr")
	if want := "Cisco IOS Software, Catalyst L3 Switch Software (CAT3K_CAA-UNIVERSALK9-M), Version 15.0(1)EX3, RELEASE SOFTWARE (fc2)\nTechnical Support: http://www.cisco.com/techsupport\nCopyright (c) 1986-2013 by Cisco Systems, Inc.\nCompiled Mon 23-Sep-13 18:24 by prod_r"; descr != want {
		t.Fatalf("chassis_descr = %q", descr)
	}
}

func TestLldpd_ManagementAddresses_AreAListAndTheFirstIsTheClient(t *testing.T) {
	line := `{"observed_at":"2026-11-20T13:06:13Z","interface":"eno2","chassis_id":"00:11:22:33:44:55","chassis_name":"switch-48.example.net","mgmt_ip":["192.0.2.39","198.51.100.7"],"port_id":"Gi4/0/18"}`

	lr, _ := runW4(t, input{body: line})

	v, ok := lr.Attributes().Get("vstan.lldp.mgmt_ip")
	if !ok || v.Type() != pcommon.ValueTypeSlice || v.Slice().Len() != 2 || v.Slice().At(1).Str() != "198.51.100.7" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	if attrStr(t, lr.Attributes(), "client.address") != "192.0.2.39" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestLldpd_NeighbourWithoutAManagementAddress_HasNoTrailingSpaceAndNoClientAddress(t *testing.T) {
	line := `{"observed_at":"2026-11-20T13:06:13Z","interface":"eno2","chassis_id":"00:11:22:33:44:55","chassis_name":"switch-48.example.net","port_id":"Gi4/0/18"}`

	lr, _ := runW4(t, input{body: line})

	w4Body(t, lr, "eno2 sees switch-48.example.net port Gi4/0/18")
	absent(t, lr.Attributes(), "client.address", "vstan.lldp.mgmt_ip")
}

func TestLldpd_JsonWithoutTheNeighbourKeys_IsNotClaimed(t *testing.T) {
	const line = `{"interface":"eno2","speed":1000}`

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}

func TestLldpd_BadObservedTime_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `{"observed_at":"yesterday","interface":"eno2","chassis_id":"00:11:22:33:44:55","chassis_name":"s","port_id":"Gi4/0/18"}`

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "lldpd")
}
