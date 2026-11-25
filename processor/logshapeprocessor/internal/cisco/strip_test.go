// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cisco

import "testing"

func TestStripPrefix(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"sequence, clock and mnemonic", "661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: Host 0000.5e00.5305 in vlan 1 is flapping", "Host 0000.5e00.5305 in vlan 1 is flapping", true},
		{"zero padded sequence", "000661: *Dec 16 00:36:36.024: %LINK-3-UPDOWN: Interface Gi0/1, changed state to down", "Interface Gi0/1, changed state to down", true},
		{"sequence and mnemonic, no clock", "661: %SYS-5-CONFIG_I: Configured from console by vty0", "Configured from console by vty0", true},
		{"clock and mnemonic, no sequence", "*Dec 16 00:36:36.024: %SYS-5-CONFIG_I: Configured from console", "Configured from console", true},
		{"unsynchronized clock mark", ".Dec 16 00:36:36: %SYS-6-CLOCKUPDATE: System clock has been updated", "System clock has been updated", true},
		{"clock with year and zone", "Dec 16 2026 00:36:36.024 UTC: %SYS-5-RESTART: System restarted", "System restarted", true},
		{"priority prefix", "<189>661: *Dec 16 00:36:36.024: %LINK-3-UPDOWN: Interface Gi0/1, changed state to up", "Interface Gi0/1, changed state to up", true},
		{"mnemonic only (switch SG)", "%COPY-N-TRAP: The copy operation was completed successfully", "The copy operation was completed successfully", true},
		{"switch SG with a clock", "Dec 16 2026 00:36:36 %AAA-I-CONNECTED: User admin connected from 192.0.2.5", "User admin connected from 192.0.2.5", true},
		{"digit mnemonic", "%ASA-6-302013: Built inbound TCP connection 1 for outside:192.0.2.1/1 to inside:192.0.2.2/2", "Built inbound TCP connection 1 for outside:192.0.2.1/1 to inside:192.0.2.2/2", true},
		{"no message left", "661: *Dec 16 00:36:36.024: %SYS-5-CONFIG_I:", "", false},
		{"no mnemonic", "661: *Dec 16 00:36:36.024: plain text", "", false},
		{"text before the mnemonic that is no sequence or clock", "switch-a 661: *Dec 16 00:36:36.024: %LINK-3-UPDOWN: up", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := StripPrefix(tt.in)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("StripPrefix(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
