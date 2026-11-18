// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"net/netip"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

const attrHostName = "host.name"

// shortHostName returns the part of name before its first dot. An IP address,
// a name with no dot and a name that starts with a dot come back unchanged.
func shortHostName(name string) string {
	i := strings.IndexByte(name, '.')
	if i <= 0 {
		return name
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return name
	}
	return name[:i]
}

// shortenHostName rewrites host.name in attrs when it holds a string.
func shortenHostName(attrs pcommon.Map) {
	v, ok := attrs.Get(attrHostName)
	if !ok || v.Type() != pcommon.ValueTypeStr {
		return
	}
	v.SetStr(shortHostName(v.Str()))
}
