// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package elbaccesslogs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The CLB splitter keeps the upstream address handling: IPv4 and the bracketed IPv6 form split, an unbracketed
// IPv6 address (more than one colon, no bracket) is not an address followed by a port.
func TestSplitAddrPortCLB(t *testing.T) {
	valid := []struct{ field, addr, port string }{
		{"192.0.2.10:2817", "192.0.2.10", "2817"},
		{"[2001:db8::1]:443", "2001:db8::1", "443"},
		{"[::1]:80", "::1", "80"},
	}
	for _, c := range valid {
		addr, port, err := splitAddrPortCLB(c.field)
		require.NoError(t, err, c.field)
		assert.Equal(t, c.addr, addr, c.field)
		assert.Equal(t, c.port, port, c.field)
	}

	for _, field := range []string{"", "-", ":80", "192.0.2.10", "2001:db8:0:1::bea9:49649", "2001:db8::1:80", "::1:443", "::ffff:192.0.2.1:8080", "[2001:db8::1]", "host.example:80"} {
		_, _, err := splitAddrPortCLB(field)
		require.Error(t, err, field)
		assert.NotContains(t, err.Error(), "2001", "the error does not hold the field")
	}
}

func TestSplitAddrPort(t *testing.T) {
	valid := []struct{ field, addr, port string }{
		{"192.0.2.10:2817", "192.0.2.10", "2817"},
		{"2001:db8:0:1::bea9:49649", "2001:db8:0:1::bea9", "49649"},
		{"2001:db8::1:80", "2001:db8::1", "80"},
		{"::1:443", "::1", "443"},
		{"::ffff:192.0.2.1:8080", "::ffff:192.0.2.1", "8080"},
		{"[2001:db8::1]:443", "2001:db8::1", "443"},
		{"[::1]:80", "::1", "80"},
	}
	for _, c := range valid {
		addr, port, err := splitAddrPort(c.field)
		require.NoError(t, err, c.field)
		assert.Equal(t, c.addr, addr, c.field)
		assert.Equal(t, c.port, port, c.field)
	}

	for _, field := range []string{"", "-", ":", ":80", "192.0.2.10", "192.0.2.10:", "2001:db8::1:", "not-an-address", "host.example:80", "[2001:db8::1]", "[2001:db8::1:80", "[bad]:80"} {
		_, _, err := splitAddrPort(field)
		require.Error(t, err, field)
		assert.NotContains(t, err.Error(), "2001", "the error does not hold the field")
		assert.NotContains(t, err.Error(), "192.0.2", "the error does not hold the field")
	}
}
