// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func TestIdentPut_KeepsTheLabelsAndReplacesRawBytes(t *testing.T) {
	attrs := pcommon.NewMap()
	ident{
		id: "S\xff1", name: "sda", model: "M\x80", serial: "S\xff1", firmware: "F\xfe", vendor: "V\xc3",
	}.put(attrs)
	want := map[string]string{
		hwschema.AttrID:              "S�1",
		hwschema.AttrName:            "sda",
		hwschema.AttrModel:           "M�",
		hwschema.AttrSerialNumber:    "S�1",
		hwschema.AttrFirmwareVersion: "F�",
		hwschema.AttrVendor:          "V�",
	}
	for key, value := range want {
		got, ok := attrs.Get(key)
		if !ok {
			t.Errorf("%s is missing: the label must be kept", key)
			continue
		}
		if got.Str() != value {
			t.Errorf("%s = %q, want %q", key, got.Str(), value)
		}
	}
}

// invalidAttributes lists every resource and data point attribute of md whose
// key or value is not valid UTF-8.
func invalidAttributes(md pmetric.Metrics) []string {
	var bad []string
	check := func(where string, m pcommon.Map) {
		m.Range(func(k string, v pcommon.Value) bool {
			if !utf8.ValidString(k) || !utf8.ValidString(v.AsString()) {
				bad = append(bad, where+": "+k)
			}
			return true
		})
	}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		check("resource", rms.At(i).Resource().Attributes())
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				var dps pmetric.NumberDataPointSlice
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					dps = m.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					dps = m.Sum().DataPoints()
				default:
					continue
				}
				for d := 0; d < dps.Len(); d++ {
					check(m.Name(), dps.At(d).Attributes())
				}
			}
		}
	}
	return bad
}

// A scrape of disks whose device name and identify strings hold raw bytes must
// send no invalid UTF-8, and the series must stay.
func TestScrape_RawBytesInDiskStringsNeverReachTheExporter(t *testing.T) {
	bus := newBus()
	bus.addATA("sd\xff-a", smarttest.HealthyATA("WD-\xff\xfe123"))
	bus.addNVMe("nvme\xfe0", smarttest.HealthyNVMe("NV\x80SN"))

	md := newHarness(t, bus).scrape()

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	if len(points(md, hwschema.MetricStatus)) == 0 {
		t.Fatal("the raw bytes dropped the hw.status series")
	}
	for _, p := range points(md, hwschema.MetricTemperature) {
		if name := p.attrs[hwschema.AttrName]; name != "sd�-a" && name != "nvme�0" {
			t.Errorf("hw.name = %q, want the raw byte replaced by U+FFFD", name)
		}
	}
}
