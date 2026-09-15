// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package macextractorprocessor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractMACs_StandardFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase with colons",
			input:    "MAC address: aa:bb:cc:dd:ee:ff",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "uppercase with colons",
			input:    "MAC address: AA:BB:CC:DD:EE:FF",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "mixed case with colons",
			input:    "MAC address: Aa:Bb:Cc:Dd:Ee:Ff",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "multiple MACs",
			input:    "from aa:bb:cc:dd:ee:ff to 11:22:33:44:55:66",
			expected: []string{"aa:bb:cc:dd:ee:ff", "11:22:33:44:55:66"},
		},
		{
			name:     "duplicate MACs",
			input:    "MAC aa:bb:cc:dd:ee:ff appears twice aa:bb:cc:dd:ee:ff",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_DashedFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase with dashes",
			input:    "MAC address: aa-bb-cc-dd-ee-ff",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "uppercase with dashes",
			input:    "MAC address: AA-BB-CC-DD-EE-FF",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "Windows ipconfig style",
			input:    "Physical Address. . . . . . . . . : 00-1A-2B-3C-4D-5E",
			expected: []string{"00:1a:2b:3c:4d:5e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_CiscoFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "lowercase Cisco format",
			input:    "MAC address: aabb.ccdd.eeff",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "uppercase Cisco format",
			input:    "MAC address: AABB.CCDD.EEFF",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "Cisco switch log",
			input:    "SYS-5-CONFIG_I: Configured from console by admin on vty0 (0050.56a3.1234)",
			expected: []string{"00:50:56:a3:12:34"},
		},
		{
			name:     "Cisco ARP entry",
			input:    "Internet  192.168.1.1  0023.04a7.8901  ARPA  FastEthernet0/0",
			expected: []string{"00:23:04:a7:89:01"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_DHCPLogs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "DHCPACK message",
			input:    "DHCPACK on 192.0.2.1 to 10:d5:61:17:80:6b (hostname) via em0",
			expected: []string{"10:d5:61:17:80:6b"},
		},
		{
			name:     "DHCPREQUEST message",
			input:    "DHCPREQUEST for 192.0.2.1 from 66:58:5b:f6:b0:30 (rdfstore-blazegraph) via em0",
			expected: []string{"66:58:5b:f6:b0:30"},
		},
		{
			name:     "DHCPDISCOVER message",
			input:    "DHCPDISCOVER from 00:0c:29:ab:cd:ef via eth0",
			expected: []string{"00:0c:29:ab:cd:ef"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_NoFalsePositives(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "IPv6 address should not match",
			input: "IPv6: 2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		},
		{
			name:  "IPv6 abbreviated",
			input: "IPv6: 2001:db8::1",
		},
		{
			name:  "short hex string",
			input: "hash: aabbcc",
		},
		{
			name:  "longer hex string",
			input: "sha256: aabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344",
		},
		{
			name:  "UUID",
			input: "uuid: 123e4567-e89b-12d3-a456-426614174000",
		},
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "no hex content",
			input: "This is just regular text without any MAC addresses",
		},
		{
			name:  "partial MAC - too short",
			input: "aa:bb:cc:dd:ee",
		},
		// Note: "aa:bb:cc:dd:ee:ff:11" DOES contain a valid MAC (aa:bb:cc:dd:ee:ff)
		// The extra ":11" after is separate. This is by design - we extract valid MACs
		// even when followed by extra content.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Empty(t, result, "Expected no MACs but got: %v", result)
		})
	}
}

func TestExtractMACs_MixedFormats(t *testing.T) {
	input := "Standard: aa:bb:cc:dd:ee:ff, Dashed: 11-22-33-44-55-66, Cisco: 0023.04a7.8901"
	expected := []string{"aa:bb:cc:dd:ee:ff", "11:22:33:44:55:66", "00:23:04:a7:89:01"}

	result := ExtractMACs(input)
	assert.Equal(t, expected, result)
}

func TestExtractFirstMAC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single MAC",
			input:    "MAC: aa:bb:cc:dd:ee:ff",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "multiple MACs returns first",
			input:    "from aa:bb:cc:dd:ee:ff to 11:22:33:44:55:66",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "no MAC",
			input:    "no mac here",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractFirstMAC(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "colon format lowercase",
			input:    "aa:bb:cc:dd:ee:ff",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "colon format uppercase",
			input:    "AA:BB:CC:DD:EE:FF",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "dash format",
			input:    "aa-bb-cc-dd-ee-ff",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "cisco format",
			input:    "aabb.ccdd.eeff",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "no separators",
			input:    "aabbccddeeff",
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "too short",
			input:    "aabbcc",
			expected: "",
		},
		{
			name:     "too long",
			input:    "aabbccddeeff00",
			expected: "",
		},
		{
			name:     "invalid characters",
			input:    "gg:hh:ii:jj:kk:ll",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMAC(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidMAC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"colon format", "aa:bb:cc:dd:ee:ff", true},
		{"dash format", "aa-bb-cc-dd-ee-ff", true},
		{"cisco format", "aabb.ccdd.eeff", true},
		{"uppercase", "AA:BB:CC:DD:EE:FF", true},
		{"empty", "", false},
		{"too short", "aa:bb:cc", false},
		{"invalid", "not-a-mac", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidMAC(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_IPv6EUI64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "fe80 link-local with EUI-64",
			input:    "fe80::225:90ff:fe8b:dba0",
			expected: []string{"00:25:90:8b:db:a0"},
		},
		{
			name:     "DHCPv6 Solicit message",
			input:    "Solicit message from fe80::225:90ff:fe8b:dba0 port 546",
			expected: []string{"00:25:90:8b:db:a0"},
		},
		{
			name:     "fe80 with different MAC",
			input:    "fe80::2ca2:deff:fe5e:7543",
			expected: []string{"2e:a2:de:5e:75:43"},
		},
		{
			name:     "uppercase EUI-64",
			input:    "fe80::AABB:CCff:feDD:EEFF",
			expected: []string{"a8:bb:cc:dd:ee:ff"},
		},
		{
			name:     "multiple EUI-64 addresses",
			input:    "from fe80::225:90ff:fe8b:dba0 to fe80::2ca2:deff:fe5e:7543",
			expected: []string{"00:25:90:8b:db:a0", "2e:a2:de:5e:75:43"},
		},
		{
			name:     "non-EUI-64 IPv6 should not match",
			input:    "fe80::1", // no ff:fe pattern
			expected: nil,
		},
		{
			name:     "global IPv6 with EUI-64 should not match",
			input:    "2001:db8::225:90ff:fe8b:dba0", // not fe80::
			expected: nil,
		},
		{
			name:     "EUI-64 with shortened hex group (leading zero dropped)",
			input:    "Sending Reply to fe80::c093:7ff:fe9f:69f4 port 546",
			expected: []string{"c2:93:07:9f:69:f4"}, // 7ff is really 07ff, MAC reconstructed with U/L bit flip
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_DUIDFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "DUID-LL type 3",
			input:    "duid 00:03:00:01:26:d9:e0:17:39:3d",
			expected: []string{"26:d9:e0:17:39:3d"},
		},
		{
			name:     "DUID-LL uppercase",
			input:    "duid 00:03:00:01:AA:BB:CC:DD:EE:FF",
			expected: []string{"aa:bb:cc:dd:ee:ff"},
		},
		{
			name:     "DUID-LLT type 1 with timestamp",
			input:    "duid 00:01:00:01:1a:2b:3c:4d:11:22:33:44:55:66",
			expected: []string{"11:22:33:44:55:66"},
		},
		{
			name:     "DHCPv6 Reply with DUID-LL",
			input:    "Reply NA: address 2603:3005:2b58:6700:0:b3ed:b897:98db to client with duid 00:03:00:01:26:d9:e0:17:39:3d iaid = 0",
			expected: []string{"26:d9:e0:17:39:3d"},
		},
		{
			name:     "DUID-LL in sentence",
			input:    "Client DUID is 00:03:00:01:c4:4f:33:91:a6:58 on interface em0",
			expected: []string{"c4:4f:33:91:a6:58"},
		},
		{
			name:     "Invalid DUID prefix should not match as DUID",
			input:    "duid 00:04:00:01:aa:bb:cc:dd:ee:ff", // type 4 doesn't exist
			expected: nil,                                  // No match because DUID type 4 is not valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMACs_CiscoSwitchLogs(t *testing.T) {
	// Real-world Cisco switch log patterns
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "CDP neighbor",
			input:    "%CDP-4-DUPLEX_MISMATCH: duplex mismatch discovered on FastEthernet0/1 (not half duplex), with Switch2 Gi0/1 (half duplex). Local MAC: 0023.04a7.8901",
			expected: []string{"00:23:04:a7:89:01"},
		},
		{
			name:     "Port security",
			input:    "%PORT_SECURITY-2-PSECURE_VIOLATION: Security violation on port Fa0/1, MAC address aabb.ccdd.1122 seen on port Fa0/2",
			expected: []string{"aa:bb:cc:dd:11:22"},
		},
		{
			name:     "MAC address table",
			input:    "   10    0050.56a3.1234    DYNAMIC     Gi0/1",
			expected: []string{"00:50:56:a3:12:34"},
		},
		{
			name:     "ARP inspection",
			input:    "%SW_DAI-4-DHCP_SNOOPING_DENY: 1 Invalid ARPs (Req) on Fa0/24, vlan 10.([0011.2233.4455/192.168.1.100/0000.0000.0000/192.168.1.1/])",
			expected: []string{"00:11:22:33:44:55", "00:00:00:00:00:00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestExtractMACs_FromTestdataFiles tests extraction using real production log samples
// These files were lifted from ELK and represent actual production data.
func TestExtractMACs_FromTestdataFiles(t *testing.T) {
	// Map of testdata filename to expected extracted MACs
	// nil means no MAC should be extracted
	testCases := map[string][]string{
		"cisco_mac_flapping.txt":     {"26:d9:e0:17:39:3d"},
		"dhcp_request_standard.txt":  {"10:db:00:db:15:02"},
		"dhcp_ack_with_hostname.txt": {"c4:4f:33:91:a6:58"},
		"dhcpv6_solicit_eui64.txt":   {"00:25:90:8b:db:a0"}, // MAC extracted from EUI-64 in fe80::225:90ff:fe8b:dba0
		"dhcpv6_reply_with_duid.txt": {"26:d9:e0:17:39:3d"}, // MAC extracted from DUID-LL
	}

	for filename, expected := range testCases {
		t.Run(filename, func(t *testing.T) {
			path := filepath.Join("testdata", filename)
			content, err := os.ReadFile(path)
			require.NoError(t, err, "Failed to read testdata file: %s", path)

			input := strings.TrimSpace(string(content))
			result := ExtractMACs(input)
			assert.Equal(t, expected, result, "File: %s", filename)
		})
	}
}

// TestExtractMACs_AdditionalDHCPPatterns tests additional DHCP patterns with hardcoded strings
// These are patterns similar to production data but not lifted directly.
func TestExtractMACs_AdditionalDHCPPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "DHCPACK standard MAC",
			input:    "DHCPACK on 192.0.2.1 to 10:db:00:db:15:02 via em0",
			expected: []string{"10:db:00:db:15:02"},
		},
		{
			name:     "DHCPREQUEST with server IP",
			input:    "DHCPREQUEST for 192.0.2.1 (192.0.2.1) from c4:4f:33:91:a6:58 (ESP_91A658) via em0",
			expected: []string{"c4:4f:33:91:a6:58"},
		},
		{
			name:     "DHCPREQUEST with Mac hostname",
			input:    "DHCPREQUEST for 192.0.2.1 from 62:12:24:a7:dd:56 (Mac) via em0",
			expected: []string{"62:12:24:a7:dd:56"},
		},
		{
			name:     "DHCPv6 Renew with IPv6 link-local EUI-64",
			input:    "Renew message from fe80::2ca2:deff:fe5e:7543 port 546, transaction ID 0xD67BC800",
			expected: []string{"2e:a2:de:5e:75:43"}, // MAC extracted from EUI-64
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMACs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func BenchmarkExtractMACs(b *testing.B) {
	input := "DHCPACK on 192.0.2.1 to 10:d5:61:17:80:6b (hostname) via em0"
	for i := 0; i < b.N; i++ {
		ExtractMACs(input)
	}
}

func BenchmarkExtractMACs_NoMatch(b *testing.B) {
	input := "This is a log message with no MAC addresses, just some text and numbers like 123456"
	for i := 0; i < b.N; i++ {
		ExtractMACs(input)
	}
}
