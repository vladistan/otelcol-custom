// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filterlogprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFilterLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid filterlog - TCP",
			input:    "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss",
			expected: true,
		},
		{
			name:     "valid filterlog - UDP",
			input:    "5,,,abc123,igb0,match,pass,out,4,0x0,,64,12345,0,DF,17,udp,60,192.168.1.1,8.8.8.8,53,53,40",
			expected: true,
		},
		{
			name:     "not filterlog - syslog message",
			input:    "Dec 16 10:30:45 fw.example.com filterlog[1234]: some message",
			expected: false,
		},
		{
			name:     "not filterlog - empty",
			input:    "",
			expected: false,
		},
		{
			name:     "not filterlog - starts with letter",
			input:    "abc,def,ghi",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFilterLog(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseFilterLog_IPv4_TCP(t *testing.T) {
	// Real filterlog entry from pfSense
	input := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"

	info := ParseFilterLog(input)
	require.NotNil(t, info)

	assert.Equal(t, "11", info.RuleID)
	assert.Equal(t, "02f4bab031b57d1e30553ce08e0ec131", info.RuleTracker)
	assert.Equal(t, "em1", info.Interface)
	assert.Equal(t, "match", info.Reason)
	assert.Equal(t, "block", info.Action)
	assert.Equal(t, "in", info.Direction)
	assert.Equal(t, "4", info.IPVersion)
	assert.Equal(t, "244", info.TTL)
	assert.Equal(t, "tcp", info.Protocol)
	assert.Equal(t, "44", info.Length)
	assert.Equal(t, "91.148.190.150", info.SourceIP)
	assert.Equal(t, "24.2.177.2", info.DestinationIP)
	assert.Equal(t, "50406", info.SourcePort)
	assert.Equal(t, "47574", info.DestinationPort)
	assert.Equal(t, "S", info.TCPFlags)

	// Check clean message
	assert.Contains(t, info.CleanMessage, "BLOCK")
	assert.Contains(t, info.CleanMessage, "em1")
	assert.Contains(t, info.CleanMessage, "inbound")
	assert.Contains(t, info.CleanMessage, "tcp")
	assert.Contains(t, info.CleanMessage, "91.148.190.150:50406")
	assert.Contains(t, info.CleanMessage, "24.2.177.2:47574")
	assert.Contains(t, info.CleanMessage, "SYN")
	assert.Contains(t, info.CleanMessage, "rule 11")
}

func TestParseFilterLog_IPv4_UDP(t *testing.T) {
	input := "5,,,abc123def456,igb0,match,pass,out,4,0x0,,64,12345,0,DF,17,udp,60,192.168.1.100,8.8.8.8,54321,53,40"

	info := ParseFilterLog(input)
	require.NotNil(t, info)

	assert.Equal(t, "5", info.RuleID)
	assert.Equal(t, "abc123def456", info.RuleTracker)
	assert.Equal(t, "igb0", info.Interface)
	assert.Equal(t, "pass", info.Action)
	assert.Equal(t, "out", info.Direction)
	assert.Equal(t, "4", info.IPVersion)
	assert.Equal(t, "udp", info.Protocol)
	assert.Equal(t, "192.168.1.100", info.SourceIP)
	assert.Equal(t, "8.8.8.8", info.DestinationIP)
	assert.Equal(t, "54321", info.SourcePort)
	assert.Equal(t, "53", info.DestinationPort)
	assert.Empty(t, info.TCPFlags)

	// Check clean message
	assert.Contains(t, info.CleanMessage, "PASS")
	assert.Contains(t, info.CleanMessage, "outbound")
	assert.Contains(t, info.CleanMessage, "udp")
}

func TestParseFilterLog_IPv4_ICMP(t *testing.T) {
	input := "10,,,tracker123,em0,match,block,in,4,0x0,,128,54321,0,none,1,icmp,84,10.0.0.1,10.0.0.2,8,12345"

	info := ParseFilterLog(input)
	require.NotNil(t, info)

	assert.Equal(t, "10", info.RuleID)
	assert.Equal(t, "em0", info.Interface)
	assert.Equal(t, "block", info.Action)
	assert.Equal(t, "icmp", info.Protocol)
	assert.Equal(t, "10.0.0.1", info.SourceIP)
	assert.Equal(t, "10.0.0.2", info.DestinationIP)
	assert.Equal(t, "8", info.ICMPType)
	assert.Equal(t, "12345", info.ICMPID)
	assert.Empty(t, info.SourcePort)
	assert.Empty(t, info.DestinationPort)

	// Check clean message
	assert.Contains(t, info.CleanMessage, "BLOCK")
	assert.Contains(t, info.CleanMessage, "icmp")
	assert.Contains(t, info.CleanMessage, "type=8")
}

func TestParseFilterLog_IPv6_TCP(t *testing.T) {
	// IPv6 filterlog entry format:
	// rule,subrule,anchor,tracker,interface,reason,action,direction,
	// ipversion,class,flowlabel,hlim,proto,length,srcip,dstip,
	// srcport,dstport,datalen,tcpflags,...
	input := "15,,,tracker456,em1,match,block,in,6,0x00,0x00000,64,tcp,60,2001:db8::1,2001:db8::2,443,54321,24,SA,123456,,65535,,mss"

	info := ParseFilterLog(input)
	require.NotNil(t, info)

	assert.Equal(t, "15", info.RuleID)
	assert.Equal(t, "tracker456", info.RuleTracker)
	assert.Equal(t, "em1", info.Interface)
	assert.Equal(t, "block", info.Action)
	assert.Equal(t, "in", info.Direction)
	assert.Equal(t, "6", info.IPVersion)
	assert.Equal(t, "tcp", info.Protocol)
	assert.Equal(t, "60", info.Length)
	assert.Equal(t, "2001:db8::1", info.SourceIP)
	assert.Equal(t, "2001:db8::2", info.DestinationIP)
	assert.Equal(t, "443", info.SourcePort)
	assert.Equal(t, "54321", info.DestinationPort)
	assert.Equal(t, "SA", info.TCPFlags)

	// Check clean message
	assert.Contains(t, info.CleanMessage, "BLOCK")
	assert.Contains(t, info.CleanMessage, "inbound")
	assert.Contains(t, info.CleanMessage, "tcp")
	assert.Contains(t, info.CleanMessage, "SYN,ACK")
}

func TestParseFilterLog_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"non-filterlog", "some random text"},
		{"too few fields", "1,2,3,4,5"},
		{"starts with letter", "abc,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseFilterLog(tt.input)
			assert.Nil(t, info)
		})
	}
}

func TestExpandTCPFlags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"S", "SYN"},
		{"SA", "SYN,ACK"},
		{"A", "ACK"},
		{"F", "FIN"},
		{"R", "RST"},
		{"P", "PSH"},
		{"U", "URG"},
		{"SAFR", "SYN,ACK,FIN,RST"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := expandTCPFlags(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeDirection(t *testing.T) {
	assert.Equal(t, "inbound", NormalizeDirection("in"))
	assert.Equal(t, "outbound", NormalizeDirection("out"))
	assert.Equal(t, "unknown", NormalizeDirection("unknown"))
}

func TestNormalizeIPVersion(t *testing.T) {
	assert.Equal(t, "ipv4", NormalizeIPVersion("4"))
	assert.Equal(t, "ipv6", NormalizeIPVersion("6"))
	assert.Equal(t, "other", NormalizeIPVersion("other"))
}

func TestBuildCleanMessage(t *testing.T) {
	tests := []struct {
		name     string
		info     *FilterLogInfo
		expected string
	}{
		{
			name: "TCP with flags",
			info: &FilterLogInfo{
				Action:          "block",
				Interface:       "em1",
				Direction:       "in",
				Protocol:        "tcp",
				SourceIP:        "1.2.3.4",
				SourcePort:      "12345",
				DestinationIP:   "5.6.7.8",
				DestinationPort: "80",
				TCPFlags:        "S",
				RuleID:          "10",
			},
			expected: "BLOCK em1 inbound tcp 1.2.3.4:12345 → 5.6.7.8:80 [SYN] (rule 10)",
		},
		{
			name: "UDP no flags",
			info: &FilterLogInfo{
				Action:          "pass",
				Interface:       "igb0",
				Direction:       "out",
				Protocol:        "udp",
				SourceIP:        "192.168.1.1",
				SourcePort:      "53",
				DestinationIP:   "8.8.8.8",
				DestinationPort: "53",
				RuleID:          "5",
			},
			expected: "PASS igb0 outbound udp 192.168.1.1:53 → 8.8.8.8:53 (rule 5)",
		},
		{
			name: "ICMP",
			info: &FilterLogInfo{
				Action:        "block",
				Interface:     "em0",
				Direction:     "in",
				Protocol:      "icmp",
				SourceIP:      "10.0.0.1",
				DestinationIP: "10.0.0.2",
				ICMPType:      "8",
				ICMPID:        "1234",
				RuleID:        "20",
			},
			expected: "BLOCK em0 inbound icmp 10.0.0.1 → 10.0.0.2 type=8 id=1234 (rule 20)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCleanMessage(tt.info)
			assert.Equal(t, tt.expected, result)
		})
	}
}
