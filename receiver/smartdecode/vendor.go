// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "strings"

// nvmeVendors maps the PCI vendor id of Identify Controller to a vendor name. An id that is not here
// has no vendor name.
var nvmeVendors = map[uint16]string{
	0x144d: "Samsung",
	0x8086: "Intel",
	0x15b7: "SanDisk",
	0x1c5c: "SK hynix",
	0x1344: "Micron",
	0xc0a9: "Micron",
	0x1179: "Toshiba",
	0x1e0f: "KIOXIA",
	0x1987: "Phison",
	0x126f: "Silicon Motion",
	0x1cc1: "ADATA",
	0x2646: "Kingston",
	0x1bb1: "Seagate",
	0x1c58: "HGST",
	0x1d0f: "Amazon",
	0x1ae0: "Google",
	0x1414: "Microsoft",
	0x1b36: "QEMU",
	0x106b: "Apple",
}

// NVMeVendor returns the vendor name for a PCI vendor id, or "" when the id is not in the table.
func NVMeVendor(id uint16) string { return nvmeVendors[id] }

// ataPrefixes maps the start of an ATA model string, compared without case, to a vendor name.
var ataPrefixes = []struct{ prefix, vendor string }{
	{"samsung", "Samsung"},
	{"intel", "Intel"},
	{"micron", "Micron"},
	{"crucial", "Crucial"},
	{"ct", "Crucial"}, // Crucial model numbers start CT followed by a digit
	{"wdc", "Western Digital"},
	{"toshiba", "Toshiba"},
	{"hgst", "HGST"},
	{"hitachi", "HGST"},
	{"kingston", "Kingston"},
	{"sandisk", "SanDisk"},
	{"sk hynix", "SK hynix"},
	{"adata", "ADATA"},
	{"qemu", "QEMU"},
	{"vbox", "VirtualBox"},
}

// ATAVendor returns the vendor name from the start of an ATA model string, or "" when the prefix is not
// in the table.
func ATAVendor(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	// Seagate model numbers are ST followed by a digit; "STORAGE" and the like are not Seagate.
	if len(m) > 2 && strings.HasPrefix(m, "st") && m[2] >= '0' && m[2] <= '9' {
		return "Seagate"
	}
	for _, p := range ataPrefixes {
		if !strings.HasPrefix(m, p.prefix) {
			continue
		}
		if p.prefix == "ct" && (len(m) < 3 || m[2] < '0' || m[2] > '9') {
			continue
		}
		return p.vendor
	}
	return ""
}
