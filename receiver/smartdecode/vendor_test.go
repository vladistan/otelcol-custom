// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "testing"

func TestParseNVMeIdentify_ReadsThePCIVendorID(t *testing.T) {
	buf := nvmeIdentifyBuffer("ACME", "S1", "FW", 1)
	buf[0], buf[1] = 0x4d, 0x14 // 0x144d, little-endian

	got, err := ParseNVMeIdentify(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.VendorID != 0x144d {
		t.Errorf("VendorID = %#x, want 0x144d", got.VendorID)
	}
}

func TestNVMeVendor(t *testing.T) {
	tests := map[string]struct {
		id   uint16
		want string
	}{
		"samsung":    {0x144d, "Samsung"},
		"intel":      {0x8086, "Intel"},
		"sandisk":    {0x15b7, "SanDisk"},
		"sk hynix":   {0x1c5c, "SK hynix"},
		"micron":     {0x1344, "Micron"},
		"kioxia":     {0x1e0f, "KIOXIA"},
		"amazon":     {0x1d0f, "Amazon"},
		"unknown id": {0xabcd, ""},
		"zero":       {0, ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := NVMeVendor(tt.id); got != tt.want {
				t.Errorf("NVMeVendor(%#x) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestATAVendor(t *testing.T) {
	tests := map[string]string{
		"Samsung SSD 860 EVO 500GB":    "Samsung",
		"SAMSUNG MZ7LH480HAHQ-00005":   "Samsung",
		"INTEL SSDSC2KB480G8":          "Intel",
		"Micron_5300_MTFDDAK480TDS":    "Micron",
		"CT500MX500SSD1":               "Crucial",
		"Crucial_CT525MX300SSD1":       "Crucial",
		"WDC WD40EFRX-68N32N0":         "Western Digital",
		"ST4000VN008-2DR166":           "Seagate",
		"TOSHIBA MG08ACA16TE":          "Toshiba",
		"HGST HUS726T6TALE6L4":         "HGST",
		"KINGSTON SA400S37480G":        "Kingston",
		"SanDisk SDSSDH3 500G":         "SanDisk",
		"QEMU HARDDISK":                "QEMU",
		"A disk nobody has heard of":   "",
		"":                             "",
		"STORAGE-X":                    "",
		"  WDC WD20EZRZ-00Z5HB0 ":      "Western Digital",
		"samsung ssd 970 evo plus 1tb": "Samsung",
	}
	for model, want := range tests {
		t.Run(model, func(t *testing.T) {
			if got := ATAVendor(model); got != want {
				t.Errorf("ATAVendor(%q) = %q, want %q", model, got, want)
			}
		})
	}
}
