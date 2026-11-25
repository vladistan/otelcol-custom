// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package resourceversionprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func attrOf(attrs pcommon.Map, key string) (string, bool) {
	v, ok := attrs.Get(key)
	if !ok {
		return "", false
	}
	return v.AsString(), true
}

func TestShortHostName_KeepsTheFullNameInHostFQDN(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("host.name", "a.b.c")

	newStamper(hostCfg(true)).stamp(attrs)

	if got, _ := attrOf(attrs, "host.name"); got != "a" {
		t.Errorf("host.name = %q, want a", got)
	}
	if got, ok := attrOf(attrs, "host.fqdn"); !ok || got != "a.b.c" {
		t.Errorf("host.fqdn = %q (present %v), want a.b.c", got, ok)
	}
}

func TestShortHostName_Off_AddsNoHostFQDN(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("host.name", "a.b.c")

	newStamper(hostCfg(false)).stamp(attrs)

	if _, ok := attrOf(attrs, "host.fqdn"); ok {
		t.Error("host.fqdn was added with short_host_name off")
	}
	if got, _ := attrOf(attrs, "host.name"); got != "a.b.c" {
		t.Errorf("host.name = %q, want it untouched", got)
	}
}

func TestShortHostName_NameThatStaysUnchanged_AddsNoHostFQDN(t *testing.T) {
	for name, host := range map[string]string{
		"ipv4": "192.0.2.10", "ipv6": "2001:db8::1", "no dot": "files", "leading dot": ".example.net", "empty": "",
	} {
		attrs := pcommon.NewMap()
		attrs.PutStr("host.name", host)

		newStamper(hostCfg(true)).stamp(attrs)

		if _, ok := attrOf(attrs, "host.fqdn"); ok {
			t.Errorf("%s: host.fqdn was added for an unchanged name", name)
		}
		if got, _ := attrOf(attrs, "host.name"); got != host {
			t.Errorf("%s: host.name = %q, want %q", name, got, host)
		}
	}
}

func TestShortHostName_NotAString_AddsNoHostFQDN(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutInt("host.name", 7)

	newStamper(hostCfg(true)).stamp(attrs)

	if _, ok := attrOf(attrs, "host.fqdn"); ok {
		t.Error("host.fqdn was added for a host.name that is not a string")
	}
}

func TestShortHostName_ResourceWithNoHostName_AddsNoHostFQDN(t *testing.T) {
	attrs := pcommon.NewMap()

	newStamper(hostCfg(true)).stamp(attrs)

	if _, ok := attrOf(attrs, "host.fqdn"); ok {
		t.Error("host.fqdn was added to a resource without host.name")
	}
}

func TestShortHostName_HostFQDNIsTheOriginalNameAlsoAfterATrailingDot(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("host.name", "web01.example.net.")

	newStamper(hostCfg(true)).stamp(attrs)

	if got, _ := attrOf(attrs, "host.name"); got != "web01" {
		t.Errorf("host.name = %q, want web01", got)
	}
	if got, _ := attrOf(attrs, "host.fqdn"); got != "web01.example.net." {
		t.Errorf("host.fqdn = %q, want the name as it came", got)
	}
}
