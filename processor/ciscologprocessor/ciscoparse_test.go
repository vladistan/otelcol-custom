// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests based on real production log samples from testdata/

func TestParseCiscoMnemonic_RealProductionSamples(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *CiscoLogInfo
	}{
		{
			// Doc 1: base-cluster3-48 (Cisco IOS switch)
			// testdata/AZsknQ9CPSMYvmTMdYE_.json
			name:  "switch_macflap",
			input: "661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: Host 6212.24a7.dd56 in vlan 1 is flapping between port Gi4/0/45 and port Gi4/0/43",
			expected: &CiscoLogInfo{
				Facility:     "SW_MATM",
				Severity:     "4",
				Mnemonic:     "MACFLAP_NOTIF",
				FullMnemonic: "%SW_MATM-4-MACFLAP_NOTIF",
			},
		},
		{
			// Doc 2: wifi-ap (Cisco WLC) - currently NOT parsed by OTTL
			// testdata/AZsk2Q9CPSMYvmRPgIRV.json
			name:  "wlc_client_associate",
			input: "DownAP-Master: *apfMsConnTask_0: Dec 15 20:49:14.582: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.",
			expected: &CiscoLogInfo{
				Facility:     "APF",
				Severity:     "5",
				Mnemonic:     "CLIENT_ASSOCIATE",
				FullMnemonic: "%APF-5-CLIENT_ASSOCIATE",
			},
		},
		{
			// Doc 3: switch-b (Cisco IOS switch)
			// testdata/AZsk5pCYuuchI90gaOpa.json
			name:  "switch_clockupdate",
			input: "171: Dec 16 02:03:21.407: %SYS-6-CLOCKUPDATE: System clock has been updated from 21:03:21 EST Mon Dec 15 2025 to 21:03:21 EST Mon Dec 15 2025, configured from console by vlad on vty0 (192.0.2.1).",
			expected: &CiscoLogInfo{
				Facility:     "SYS",
				Severity:     "6",
				Mnemonic:     "CLOCKUPDATE",
				FullMnemonic: "%SYS-6-CLOCKUPDATE",
			},
		},
		{
			// Doc 4: switch-a (Cisco IOS) - bare mnemonic, letter severity
			// testdata/AZsk8w9CPSMYvmTcha_Z.json
			name:  "switch_bare_mnemonic_letter_severity",
			input: "%COPY-N-TRAP: The copy operation was completed successfully",
			expected: &CiscoLogInfo{
				Facility:     "COPY",
				Severity:     "N",
				Mnemonic:     "TRAP",
				FullMnemonic: "%COPY-N-TRAP",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCiscoMnemonic(tt.input)
			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}
			assert.NotNil(t, result, "Expected to parse: %s", tt.input)
			assert.Equal(t, tt.expected.Facility, result.Facility)
			assert.Equal(t, tt.expected.Severity, result.Severity)
			assert.Equal(t, tt.expected.Mnemonic, result.Mnemonic)
			assert.Equal(t, tt.expected.FullMnemonic, result.FullMnemonic)
		})
	}
}

func TestParseCiscoMnemonic_VariousFormats(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *CiscoLogInfo
	}{
		{
			name:  "basic_mnemonic",
			input: "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
			expected: &CiscoLogInfo{
				Facility:     "LINK",
				Severity:     "3",
				Mnemonic:     "UPDOWN",
				FullMnemonic: "%LINK-3-UPDOWN",
			},
		},
		{
			name:  "with_syslog_priority",
			input: "<189>%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
			expected: &CiscoLogInfo{
				Facility:     "LINK",
				Severity:     "3",
				Mnemonic:     "UPDOWN",
				FullMnemonic: "%LINK-3-UPDOWN",
			},
		},
		{
			name:  "mnemonic_with_numbers",
			input: "%ASA-6-302013: Built inbound TCP connection",
			expected: &CiscoLogInfo{
				Facility:     "ASA",
				Severity:     "6",
				Mnemonic:     "302013",
				FullMnemonic: "%ASA-6-302013",
			},
		},
		{
			name:  "underscore_in_facility",
			input: "%PORT_SECURITY-2-PSECURE_VIOLATION: Security violation occurred",
			expected: &CiscoLogInfo{
				Facility:     "PORT_SECURITY",
				Severity:     "2",
				Mnemonic:     "PSECURE_VIOLATION",
				FullMnemonic: "%PORT_SECURITY-2-PSECURE_VIOLATION",
			},
		},
		{
			name:  "severity_letter_I",
			input: "%PLATFORM-I-INFO: Some informational message",
			expected: &CiscoLogInfo{
				Facility:     "PLATFORM",
				Severity:     "I",
				Mnemonic:     "INFO",
				FullMnemonic: "%PLATFORM-I-INFO",
			},
		},
		{
			name:  "severity_letter_W",
			input: "%SYS-W-WARNING: Some warning message",
			expected: &CiscoLogInfo{
				Facility:     "SYS",
				Severity:     "W",
				Mnemonic:     "WARNING",
				FullMnemonic: "%SYS-W-WARNING",
			},
		},
		{
			name:  "severity_letter_E",
			input: "%KERNEL-E-ERROR: Some error message",
			expected: &CiscoLogInfo{
				Facility:     "KERNEL",
				Severity:     "E",
				Mnemonic:     "ERROR",
				FullMnemonic: "%KERNEL-E-ERROR",
			},
		},
		{
			name:  "mnemonic_deep_in_message",
			input: "some prefix text here: %TEST-5-MNEMONIC: actual message",
			expected: &CiscoLogInfo{
				Facility:     "TEST",
				Severity:     "5",
				Mnemonic:     "MNEMONIC",
				FullMnemonic: "%TEST-5-MNEMONIC",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCiscoMnemonic(tt.input)
			assert.NotNil(t, result, "Expected to parse: %s", tt.input)
			assert.Equal(t, tt.expected.Facility, result.Facility)
			assert.Equal(t, tt.expected.Severity, result.Severity)
			assert.Equal(t, tt.expected.Mnemonic, result.Mnemonic)
			assert.Equal(t, tt.expected.FullMnemonic, result.FullMnemonic)
		})
	}
}

func TestParseCiscoMnemonic_NoMatch(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty_string",
			input: "",
		},
		{
			name:  "regular_syslog",
			input: "Oct 15 09:23:45 server sshd[1234]: Accepted password for user",
		},
		{
			name:  "hp_switch_format",
			input: " Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: usmdb_sim.c(3847) 2805 %% Event(0x0)",
		},
		{
			name:  "no_percent_sign",
			input: "LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
		},
		{
			name:  "partial_mnemonic_no_severity",
			input: "%LINK-UPDOWN: missing severity",
		},
		{
			name:  "partial_mnemonic_no_mnemonic",
			input: "%LINK-3: missing mnemonic part",
		},
		{
			name:  "double_percent_hp",
			input: "%% Event(0x0)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCiscoMnemonic(tt.input)
			assert.Nil(t, result, "Expected nil for: %s", tt.input)
		})
	}
}

func TestParseCiscoMnemonic_AllDigitSeverities(t *testing.T) {
	// Verify all numeric severity levels 0-7 are parsed
	for sev := 0; sev <= 7; sev++ {
		sevStr := string(rune('0' + sev))
		input := "%TEST-" + sevStr + "-MNEMONIC: Test message"
		result := ParseCiscoMnemonic(input)
		assert.NotNil(t, result, "Should parse severity %s", sevStr)
		assert.Equal(t, sevStr, result.Severity)
	}
}

// HP Switch parsing tests

func TestParseHPLog_RealProductionSample(t *testing.T) {
	// Doc 5: switch-c (HP switch)
	// testdata/AZslAJCYuuchI90RbtY8.json
	input := " Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: usmdb_sim.c(3847) 2805 %% Event(0x0)"

	result := ParseHPLog(input)
	assert.NotNil(t, result)
	assert.Equal(t, "switch-c-1", result.Hostname)
	assert.Equal(t, "General", result.AppName)
	assert.Equal(t, "tRpcsrv.00001", result.ProcessID)
	assert.Equal(t, "usmdb_sim.c", result.CodeFilepath)
	assert.Equal(t, "3847", result.CodeLineno)
	assert.Equal(t, "2805", result.LogSequence)
	assert.Equal(t, "Event(0x0)", result.CleanMessage)
}

func TestParseHPLog_CodeLocationExtraction(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		codeFilepath string
		codeLineno   string
		logSequence  string
		cleanMessage string
	}{
		{
			name:         "spanning_tree_topology_change",
			input:        " Dec 15 22:56:14 switch-c-1 TRAPMGR[dot1s_task]: traputil.c(763) 284 %% Spanning Tree Topology Change Received: MSTID: 0 1",
			codeFilepath: "traputil.c",
			codeLineno:   "763",
			logSequence:  "284",
			cleanMessage: "Spanning Tree Topology Change Received: MSTID: 0 1",
		},
		{
			name:         "event_with_header_file",
			input:        " Dec 15 21:31:30 switch-c-1 General[task123]: events.h(100) 500 %% Some event happened",
			codeFilepath: "events.h",
			codeLineno:   "100",
			logSequence:  "500",
			cleanMessage: "Some event happened",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseHPLog(tt.input)
			assert.NotNil(t, result)
			assert.Equal(t, tt.codeFilepath, result.CodeFilepath)
			assert.Equal(t, tt.codeLineno, result.CodeLineno)
			assert.Equal(t, tt.logSequence, result.LogSequence)
			assert.Equal(t, tt.cleanMessage, result.CleanMessage)
		})
	}
}

func TestParseHPLog_NoMatch(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "cisco_log",
			input: "%SW_MATM-4-MACFLAP_NOTIF: Host flapping",
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "regular_syslog",
			input: "Dec 15 21:31:30 server app: message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseHPLog(tt.input)
			assert.Nil(t, result, "Expected nil for: %s", tt.input)
		})
	}
}

// Body cleanup tests

func TestCleanBody(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "remove_priority_prefix",
			input:    "<189>%COPY-N-TRAP: The copy operation was completed successfully",
			expected: "%COPY-N-TRAP: The copy operation was completed successfully",
		},
		{
			name:     "remove_priority_with_space",
			input:    "<10> Dec 15 21:31:30 switch-c-1 General",
			expected: "Dec 15 21:31:30 switch-c-1 General",
		},
		{
			name:     "no_priority",
			input:    "Just a regular message",
			expected: "Just a regular message",
		},
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanBody(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Interface name extraction tests

func TestExtractInterfaceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "gigabit_full",
			input:    "%LINK-3-UPDOWN: Interface GigabitEthernet0/1, changed state to up",
			expected: "GigabitEthernet0/1",
		},
		{
			name:     "gigabit_three_level",
			input:    "Host flapping between port Gi4/0/45 and port Gi4/0/43",
			expected: "Gi4/0/45",
		},
		{
			name:     "gigabit_short_two_level",
			input:    "Interface Gi0/1 is down",
			expected: "Gi0/1",
		},
		{
			name:     "gigabit_short_single",
			input:    "%STP-W-PORTSTATUS: gi4: STP status Forwarding",
			expected: "gi4",
		},
		{
			name:     "fast_ethernet",
			input:    "Interface FastEthernet0/24 is up",
			expected: "FastEthernet0/24",
		},
		{
			name:     "vlan",
			input:    "Interface Vlan10 is up/up",
			expected: "Vlan10",
		},
		{
			name:     "port_channel",
			input:    "Port-channel1 state changed",
			expected: "Port-channel1",
		},
		{
			name:     "ten_gigabit",
			input:    "TenGigabitEthernet1/0/1 link down",
			expected: "TenGigabitEthernet1/0/1",
		},
		{
			name:     "no_interface",
			input:    "System clock updated",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractInterfaceName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAllInterfaceNames(t *testing.T) {
	input := "Host flapping between port Gi4/0/45 and port Gi4/0/43"
	result := ExtractAllInterfaceNames(input)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "Gi4/0/45")
	assert.Contains(t, result, "Gi4/0/43")
}

// WLC (Wireless LAN Controller) parsing tests

func TestParseWLCLog_RealProductionSample(t *testing.T) {
	// Real WLC log from wifi-ap.home.example.com
	input := "DownAP-Master: *apfMsConnTask_0: Dec 15 23:44:44.693: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1."

	result := ParseWLCLog(input)
	assert.NotNil(t, result)
	assert.Equal(t, "DownAP-Master", result.APName)
	assert.Equal(t, "apfMsConnTask_0", result.ThreadName)
	assert.Equal(t, "apf_80211.c", result.CodeFilepath)
	assert.Equal(t, "13338", result.CodeLineno)
	assert.Equal(t, "CiscoAP-Down", result.ClientAP)
	assert.Equal(t, "2.4GHz", result.Radio)
	assert.Equal(t, "1", result.WLANId)
	// Clean message should have extracted fields removed
	assert.NotContains(t, result.CleanMessage, "Client MAC:")
	assert.NotContains(t, result.CleanMessage, "AP Name:")
	assert.NotContains(t, result.CleanMessage, "WLAN Id:")
	assert.Contains(t, result.CleanMessage, "Client Association")
}

func TestParseWLCLog_FieldExtraction(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		apName       string
		threadName   string
		codeFilepath string
		codeLineno   string
		clientAP     string
		radio        string
		wlanId       string
	}{
		{
			name:         "client_associate",
			input:        "DownAP-Master: *apfMsConnTask_0: Dec 15 20:49:14.582: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.",
			apName:       "DownAP-Master",
			threadName:   "apfMsConnTask_0",
			codeFilepath: "apf_80211.c",
			codeLineno:   "13338",
			clientAP:     "CiscoAP-Down",
			radio:        "2.4GHz",
			wlanId:       "1",
		},
		{
			name:         "client_deauth",
			input:        "CiscoAP-Upstairs: *apfMsConnTask_1: Dec 16 01:15:22.123: %APF-6-CLIENT_DEAUTH: apf_auth.c:5432 Client Deauthentication: AP Name: CiscoAP-Upstairs, Radio: 5GHz , WLAN Id: 2.",
			apName:       "CiscoAP-Upstairs",
			threadName:   "apfMsConnTask_1",
			codeFilepath: "apf_auth.c",
			codeLineno:   "5432",
			clientAP:     "CiscoAP-Upstairs",
			radio:        "5GHz",
			wlanId:       "2",
		},
		{
			name:         "no_code_location",
			input:        "TestAP: *someThread: Dec 16 02:00:00.000: %DOT1X-5-EVENT: Some dot1x event: AP Name: TestAP, Radio: 2.4GHz .",
			apName:       "TestAP",
			threadName:   "someThread",
			codeFilepath: "",
			codeLineno:   "",
			clientAP:     "TestAP",
			radio:        "2.4GHz",
			wlanId:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseWLCLog(tt.input)
			assert.NotNil(t, result)
			assert.Equal(t, tt.apName, result.APName)
			assert.Equal(t, tt.threadName, result.ThreadName)
			assert.Equal(t, tt.codeFilepath, result.CodeFilepath)
			assert.Equal(t, tt.codeLineno, result.CodeLineno)
			assert.Equal(t, tt.clientAP, result.ClientAP)
			assert.Equal(t, tt.radio, result.Radio)
			assert.Equal(t, tt.wlanId, result.WLANId)
		})
	}
}

func TestParseWLCLog_NoMatch(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "regular_cisco_log",
			input: "%SW_MATM-4-MACFLAP_NOTIF: Host flapping",
		},
		{
			name:  "hp_switch_log",
			input: " Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]: usmdb_sim.c(3847) 2805 %% Event(0x0)",
		},
		{
			name:  "regular_syslog",
			input: "Dec 15 21:31:30 server app: message",
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "missing_asterisk",
			input: "DownAP-Master: apfMsConnTask_0: Dec 15 23:44:44.693: %APF-5-CLIENT_ASSOCIATE:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseWLCLog(tt.input)
			assert.Nil(t, result, "Expected nil for: %s", tt.input)
		})
	}
}

func TestIsWLCLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "wlc_log",
			input:    "DownAP-Master: *apfMsConnTask_0: Dec 15 23:44:44.693: %APF-5-CLIENT_ASSOCIATE:",
			expected: true,
		},
		{
			name:     "regular_cisco",
			input:    "%SW_MATM-4-MACFLAP_NOTIF: Host flapping",
			expected: false,
		},
		{
			name:     "hp_switch",
			input:    " Dec 15 21:31:30 switch-c-1 General[tRpcsrv.00001]:",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsWLCLog(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCleanWLCMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full_extraction",
			input:    "Client Association: Client MAC: c4:4f:33:91:a6:58, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.",
			expected: "Client Association",
		},
		{
			name:     "partial_extraction",
			input:    "Some Event: AP Name: TestAP, Radio: 5GHz .",
			expected: "Some Event",
		},
		{
			name:     "no_fields_to_extract",
			input:    "Simple message without fields",
			expected: "Simple message without fields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanWLCMessage(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
