// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package elbaccesslogs // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension/internal/unmarshaler/elb-access-log"

import (
	"errors"
	"net"
	"net/netip"
	"strings"
)

var errNoAddrPort = errors.New("not an address followed by a port")

// splitAddrPort splits an address:port field of an ELB access log line (client, target, destination or backend).
// An IPv6 address is written without brackets (2001:db8::1:49649), so the field is split at the last colon; the
// bracketed form [2001:db8::1]:443 splits as before. The address must be an IP address. The error does not hold
// the field.
func splitAddrPort(field string) (addr, port string, err error) {
	if strings.HasPrefix(field, "[") {
		if addr, port, err = net.SplitHostPort(field); err != nil {
			return "", "", errNoAddrPort
		}
	} else {
		i := strings.LastIndexByte(field, ':')
		if i <= 0 || i == len(field)-1 {
			return "", "", errNoAddrPort
		}
		addr, port = field[:i], field[i+1:]
	}
	if _, err = netip.ParseAddr(addr); err != nil {
		return "", "", errNoAddrPort
	}
	return addr, port, nil
}
