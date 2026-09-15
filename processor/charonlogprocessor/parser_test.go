// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package charonlogprocessor

import (
	"testing"
)

func TestIsCharonLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid charon IKE log",
			input:    "13[IKE] <efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|82> CHILD_SA homelan closed",
			expected: true,
		},
		{
			name:     "valid charon NET log",
			input:    "14[NET] <home-to-aws|5> received packet: from 1.2.3.4[500] to 10.0.0.1[500] (220 bytes)",
			expected: true,
		},
		{
			name:     "valid charon ENC log",
			input:    "09[ENC] <vpn|1> parsed IKE_AUTH response",
			expected: true,
		},
		{
			name:     "valid charon CFG log",
			input:    "01[CFG] <conn-name|> loaded connection 'home-to-aws'",
			expected: true,
		},
		{
			name:     "too short",
			input:    "1[IKE]",
			expected: false,
		},
		{
			name:     "no bracket pattern",
			input:    "This is a normal log message",
			expected: false,
		},
		{
			name:     "no angle brackets",
			input:    "13[IKE] some message without connection",
			expected: false,
		},
		{
			name:     "not starting with digit",
			input:    "IKE[13] <conn|1> message",
			expected: false,
		},
		{
			name:     "syslog wrapped charon - just the charon part",
			input:    "06[CHD] <home-to-aws|123> CHILD_SA established",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCharonLog(tt.input)
			if result != tt.expected {
				t.Errorf("IsCharonLog(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseCharonLog(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantNil       bool
		wantThreadID  string
		wantComponent string
		wantConnID    string
		wantConnSeq   string
		wantMessage   string
		wantExtraKeys []string
		wantExtraVals map[string]string
	}{
		{
			name:          "basic IKE log",
			input:         "13[IKE] <efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7|82> establishing CHILD_SA homelan",
			wantNil:       false,
			wantThreadID:  "13",
			wantComponent: "IKE",
			wantConnID:    "efd7edbd-4bdf-46bd-b17c-c1d569b4c5a7",
			wantConnSeq:   "82",
			wantMessage:   "establishing CHILD_SA homelan",
		},
		{
			name:          "received packet",
			input:         "14[NET] <home-to-aws|5> received packet: from 203.0.113.50[4500] to 10.0.0.1[4500] (220 bytes)",
			wantNil:       false,
			wantThreadID:  "14",
			wantComponent: "NET",
			wantConnID:    "home-to-aws",
			wantConnSeq:   "5",
			wantExtraKeys: []string{"ipsec.direction", "ipsec.remote_ip", "ipsec.local_ip", "ipsec.packet_size"},
			wantExtraVals: map[string]string{
				"ipsec.direction":   "received",
				"ipsec.remote_ip":   "203.0.113.50",
				"ipsec.remote_port": "4500",
				"ipsec.local_ip":    "10.0.0.1",
				"ipsec.local_port":  "4500",
				"ipsec.packet_size": "220",
			},
		},
		{
			name:          "sending packet",
			input:         "08[NET] <vpn|2> sending packet: from 192.168.1.1[500] to 8.8.8.8[500] (368 bytes)",
			wantNil:       false,
			wantThreadID:  "08",
			wantComponent: "NET",
			wantExtraVals: map[string]string{
				"ipsec.direction":   "sending",
				"ipsec.local_ip":    "192.168.1.1",
				"ipsec.local_port":  "500",
				"ipsec.remote_ip":   "8.8.8.8",
				"ipsec.remote_port": "500",
				"ipsec.packet_size": "368",
			},
		},
		{
			name:          "CHILD_SA established",
			input:         "11[CHD] <home-to-aws|6> CHILD_SA abc12345-1234-1234-1234-123456789abc{7} established",
			wantNil:       false,
			wantThreadID:  "11",
			wantComponent: "CHD",
			wantExtraKeys: []string{"ipsec.child_sa", "ipsec.child_sa_num", "ipsec.child_sa_state"},
			wantExtraVals: map[string]string{
				"ipsec.child_sa":       "abc12345-1234-1234-1234-123456789abc",
				"ipsec.child_sa_num":   "7",
				"ipsec.child_sa_state": "established",
			},
		},
		{
			name:    "SPI info",
			input:   "12[IKE] <conn|3> SPIs c1234567_i d7654321_o selected",
			wantNil: false,
			wantExtraVals: map[string]string{
				"ipsec.spi_in":  "c1234567",
				"ipsec.spi_out": "d7654321",
			},
		},
		{
			name:    "DELETE message",
			input:   "10[IKE] <conn|4> received DELETE for ESP CHILD_SA with SPI abcd1234",
			wantNil: false,
			wantExtraVals: map[string]string{
				"ipsec.delete_protocol": "ESP",
				"ipsec.delete_spi":      "abcd1234",
			},
		},
		{
			name:    "authentication successful",
			input:   "07[IKE] <conn|5> authentication of 'CN=vpn.example.com' with RSA_EMSA_PKCS1_SHA2_256 successful",
			wantNil: false,
			wantExtraVals: map[string]string{
				"ipsec.auth_identity": "CN=vpn.example.com",
				"ipsec.auth_method":   "RSA_EMSA_PKCS1_SHA2_256",
				"ipsec.auth_result":   "successful",
			},
		},
		{
			name:    "IKE_SA established",
			input:   "05[IKE] <home-to-aws|7> IKE_SA home-to-aws[123] established",
			wantNil: false,
			wantExtraVals: map[string]string{
				"ipsec.ike_sa_name":  "home-to-aws",
				"ipsec.ike_sa_num":   "123",
				"ipsec.ike_sa_state": "established",
			},
		},
		{
			name:    "selected proposal",
			input:   "04[IKE] <conn|8> selected proposal: ESP:AES_GCM_16_256/NO_EXT_SEQ",
			wantNil: false,
			wantExtraVals: map[string]string{
				"ipsec.proposal": "ESP:AES_GCM_16_256/NO_EXT_SEQ",
			},
		},
		{
			name:          "connection ID without sequence",
			input:         "02[CFG] <conn-name|> loading configuration",
			wantNil:       false,
			wantThreadID:  "02",
			wantComponent: "CFG",
			wantConnID:    "conn-name",
			wantConnSeq:   "",
			wantMessage:   "loading configuration",
		},
		{
			name:    "invalid format",
			input:   "This is not a charon log",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCharonLog(tt.input)

			if tt.wantNil {
				if result != nil {
					t.Errorf("ParseCharonLog(%q) = %+v, want nil", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("ParseCharonLog(%q) = nil, want non-nil", tt.input)
			}

			if tt.wantThreadID != "" && result.ThreadID != tt.wantThreadID {
				t.Errorf("ThreadID = %q, want %q", result.ThreadID, tt.wantThreadID)
			}
			if tt.wantComponent != "" && result.Component != tt.wantComponent {
				t.Errorf("Component = %q, want %q", result.Component, tt.wantComponent)
			}
			if tt.wantConnID != "" && result.ConnectionID != tt.wantConnID {
				t.Errorf("ConnectionID = %q, want %q", result.ConnectionID, tt.wantConnID)
			}
			if tt.wantConnSeq != "" && result.ConnectionSeq != tt.wantConnSeq {
				t.Errorf("ConnectionSeq = %q, want %q", result.ConnectionSeq, tt.wantConnSeq)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.wantMessage)
			}

			for key, wantVal := range tt.wantExtraVals {
				gotVal, ok := result.Extra[key]
				if !ok {
					t.Errorf("Extra[%q] not found, want %q", key, wantVal)
				} else if gotVal != wantVal {
					t.Errorf("Extra[%q] = %q, want %q", key, gotVal, wantVal)
				}
			}
		})
	}
}

func TestComponentToDescription(t *testing.T) {
	tests := []struct {
		component string
		expected  string
	}{
		{"IKE", "IKE Protocol"},
		{"NET", "Network"},
		{"ENC", "Encoding"},
		{"CFG", "Configuration"},
		{"CHD", "Child SA"},
		{"JOB", "Job Scheduler"},
		{"KNL", "Kernel Interface"},
		{"MGR", "SA Manager"},
		{"ASN", "ASN.1 Parser"},
		{"LIB", "Library"},
		{"TLS", "TLS"},
		{"ESP", "ESP Protocol"},
		{"AH", "AH Protocol"},
		{"UNKNOWN", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.component, func(t *testing.T) {
			result := ComponentToDescription(tt.component)
			if result != tt.expected {
				t.Errorf("ComponentToDescription(%q) = %q, want %q", tt.component, result, tt.expected)
			}
		})
	}
}
