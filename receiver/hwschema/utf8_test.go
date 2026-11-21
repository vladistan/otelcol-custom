// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestValidUTF8ReplacesEachInvalidByte(t *testing.T) {
	cases := map[string]string{
		"WD-WCC4N1234567":        "WD-WCC4N1234567",
		"WD-\xff\xfe123":         "WD-��123",
		"\x80":                   "�",
		"Samsung SSD \xe2\x82":   "Samsung SSD ��",
		"café 中":                 "café 中",
		"":                       "",
		"a\x00b":                 "a\x00b",
		"\xf0\x9f\x98":           "���",
		"ok\xc3\x28end":          "ok�(end",
		"\xed\xa0\x80 surrogate": "��� surrogate",
	}
	for in, want := range cases {
		got := ValidUTF8(in)
		if got != want {
			t.Errorf("ValidUTF8(%q) = %q, want %q", in, got, want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("ValidUTF8(%q) = %q is not valid UTF-8", in, got)
		}
	}
}

func TestValidUTF8DoesNotAllocateForValidText(t *testing.T) {
	const s = "ST4000NM0035-1V4107"
	if n := testing.AllocsPerRun(100, func() { _ = ValidUTF8(s) }); n != 0 {
		t.Errorf("ValidUTF8 allocated %v times for valid text", n)
	}
}

func TestBuilderKeepsTheLabelsAndReplacesInvalidBytes(t *testing.T) {
	res := Resource{HostName: "h", Model: "M\xff1", Serial: "WD-\xff\xfe123", Firmware: "FW\x80"}
	b := NewBuilder("test", res, time.Unix(1, 0))
	part := b.Component(map[string]string{AttrID: "id-\xfe", AttrSerialNumber: "S\xff"})
	rms := part.Metrics().ResourceMetrics()
	want := []map[string]string{
		{AttrModel: "M�1", AttrSerialNumber: "WD-��123", AttrFirmwareVersion: "FW�"},
		{AttrModel: "M�1", AttrSerialNumber: "S�", AttrFirmwareVersion: "FW�", AttrID: "id-�"},
	}
	if rms.Len() != len(want) {
		t.Fatalf("got %d resources, want %d", rms.Len(), len(want))
	}
	for i, w := range want {
		attrs := rms.At(i).Resource().Attributes()
		for key, value := range w {
			got, ok := attrs.Get(key)
			if !ok {
				t.Errorf("resource %d lacks %s: the label must be kept", i, key)
				continue
			}
			if got.Str() != value {
				t.Errorf("resource %d %s = %q, want %q", i, key, got.Str(), value)
			}
		}
	}
}

// invalidAttribute returns the first resource or data point attribute of md
// that holds an invalid UTF-8 value, as "key=value", or "" when all are valid.
func invalidAttribute(md pmetric.Metrics) string {
	bad := ""
	check := func(m pcommon.Map) {
		m.Range(func(k string, v pcommon.Value) bool {
			if !utf8.ValidString(k) || !utf8.ValidString(v.AsString()) {
				bad = fmt.Sprintf("%q=%q", k, v.AsString())
				return false
			}
			return true
		})
	}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len() && bad == ""; i++ {
		check(rms.At(i).Resource().Attributes())
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len() && bad == ""; j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len() && bad == ""; k++ {
				m := ms.At(k)
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					for d := 0; d < m.Gauge().DataPoints().Len() && bad == ""; d++ {
						check(m.Gauge().DataPoints().At(d).Attributes())
					}
				case pmetric.MetricTypeSum:
					for d := 0; d < m.Sum().DataPoints().Len() && bad == ""; d++ {
						check(m.Sum().DataPoints().At(d).Attributes())
					}
				}
			}
		}
	}
	return bad
}

func TestEveryBuilderAttributeIsValidUTF8(t *testing.T) {
	const raw = "bad\xff\xfe"
	res := Resource{HostName: raw, Vendor: raw, ServerAddress: raw, Protocol: raw, Model: raw, Serial: raw, Firmware: raw}
	b := NewBuilder("test", res, time.Unix(1, 0))
	b.Temperature(raw, raw, raw, 40)
	b.TemperatureLimit(raw, raw, raw, raw, 80)
	b.FanSpeed(raw, raw, raw, 1000)
	b.Voltage(raw, raw, raw, 12)
	b.PhysicalDiskSize(raw, raw, raw, 1e9)
	b.PhysicalDiskIndicator(raw, raw, raw, true)
	b.StorageDriveCount(raw, raw, 2)
	b.Status("physical_disk", raw, raw, States[0])
	b.StatusWith("physical_disk", raw, raw, States[0], map[string]string{"extra.key": raw})
	b.LogicalDiskLimit(raw, raw, raw, 1e9, map[string]string{"extra.key": raw})
	b.FRUInfo(raw, map[string]string{"fru.serial": raw})
	b.ScrapeErrors(raw, 1)
	b.BMCUp(false, raw)
	b.InventoryStatus(raw, true)
	b.Component(map[string]string{AttrVendor: raw, AttrID: raw}).Temperature(raw, raw, raw, 1)
	if bad := invalidAttribute(b.Metrics()); bad != "" {
		t.Errorf("an attribute holds invalid UTF-8: %s", bad)
	}
}

func TestIdentityReplacesRawBytesInIDNameAndParent(t *testing.T) {
	b := NewBuilder("test", Resource{HostName: "h"}, time.Unix(1, 0))
	b.Temperature("id\xff", "name\xfe", "par\x80ent", 40)
	dp := b.Metrics().ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	want := map[string]string{AttrID: "id�", AttrName: "name�", AttrParent: "par�ent"}
	for k, v := range want {
		got, _ := dp.Attributes().Get(k)
		if got.Str() != v {
			t.Errorf("%s = %q, want %q", k, got.Str(), v)
		}
	}
}

func TestValidUTF8SizesTheBufferForManyInvalidBytes(t *testing.T) {
	in := strings.Repeat("\xff", 64)
	if n := testing.AllocsPerRun(50, func() { _ = ValidUTF8(in) }); n > 2 {
		t.Errorf("ValidUTF8 allocated %v times for 64 invalid bytes, want at most 2", n)
	}
}
