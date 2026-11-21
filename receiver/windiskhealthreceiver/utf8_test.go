// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func TestCString_RawBytesBecomeReplacementCharacters(t *testing.T) {
	buf := append(make([]byte, 4), []byte("WD-\xff\xfe123 \x00tail")...)
	got, err := cString(buf, 4)
	if err != nil {
		t.Fatalf("cString: %v", err)
	}
	if want := "WD-��123"; got != want {
		t.Errorf("cString = %q, want %q", got, want)
	}
}

func TestIdentPut_KeepsTheLabelsAndReplacesRawBytes(t *testing.T) {
	attrs := pcommon.NewMap()
	ident{
		id: "S\xff1", name: "PhysicalDrive0", model: "M\x80", serial: "S\xff1", firmware: "F\xfe", diskType: hwschema.DiskHDD,
	}.put(attrs)
	want := map[string]string{
		hwschema.AttrID:              "S�1",
		hwschema.AttrName:            "PhysicalDrive0",
		hwschema.AttrModel:           "M�",
		hwschema.AttrSerialNumber:    "S�1",
		hwschema.AttrFirmwareVersion: "F�",
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

// A scrape of disks whose descriptor, identify data and device name hold raw
// bytes must send no invalid UTF-8, and the series must stay.
func TestScrape_RawBytesInDiskStringsNeverReachTheExporter(t *testing.T) {
	b := newFakeBus()
	sata := sataDisk(smarttest.HealthyATA("WD-\xff\xfe123"))
	sata.desc = descriptor{busType: 11, vendor: "AC\x80ME", product: "SSD\xc3", firmware: "F\xfe1", serial: "WD-\xff\xfe123"}
	b.add("Physical\xffDrive0", sata)
	nvme := nvmeDisk(smarttest.HealthyNVMe("NV\xffSN"))
	nvme.desc.serial = "NV\xffSN"
	b.add("Physical\xfeDrive1", nvme)

	md := newHarness(t, b).scrape()

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	if got := len(points(md, hwschema.MetricStatus)); got == 0 {
		t.Fatal("the raw bytes dropped the hw.status series")
	}
	for key := range points(md, hwschema.MetricTemperature) {
		if !utf8.ValidString(key) {
			t.Errorf("temperature series key %q is not valid UTF-8", key)
		}
	}
}
