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

// splitAddrPort splits an address:port field of an ALB or NLB access log line (client, target or destination).
// An IPv6 address is written without brackets (2001:db8::1:49649), so the field is split at the last colon; the
// bracketed form [2001:db8::1]:443 splits as before. The address must be an IP address. The error does not hold
// the field.
// splitAddrPortCLB is splitAddrPort for CLB lines, which stay outside the IPv6 fix until a live CLB line with an
// IPv6 address is on record: IPv4 and the bracketed form split, and an unbracketed IPv6 address is a bad field.
func splitAddrPortCLB(field string) (addr, port string, err error) {
	if !strings.HasPrefix(field, "[") && strings.Count(field, ":") > 1 {
		return "", "", errNoAddrPort
	}
	return splitAddrPort(field)
}

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
