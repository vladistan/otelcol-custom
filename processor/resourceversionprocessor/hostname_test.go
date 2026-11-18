// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import "testing"

func TestShortHostName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"fqdn", "web01.example.net", "web01"},
		{"deep fqdn", "a.b.c.example.net", "a"},
		{"short", "files", "files"},
		{"single label with digits", "web01", "web01"},
		{"empty", "", ""},
		{"ipv4", "192.0.2.10", "192.0.2.10"},
		{"ipv6", "2001:db8::1", "2001:db8::1"},
		{"ipv6 loopback", "::1", "::1"},
		{"ipv6 with zone", "fe80::1%eth0", "fe80::1%eth0"},
		{"ipv4 mapped ipv6", "::ffff:192.0.2.10", "::ffff:192.0.2.10"},
		{"trailing dot", "host.example.net.", "host"},
		{"leading dot", ".example.net", ".example.net"},
		{"only a dot", ".", "."},
		{"upper case kept", "Host.Example.NET", "Host"},
		{"first label empty after dot dot", "..example", "..example"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortHostName(tc.in); got != tc.want {
				t.Errorf("shortHostName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
