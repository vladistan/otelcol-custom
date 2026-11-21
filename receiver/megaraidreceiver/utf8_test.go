// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

func TestIdentPut_KeepsTheLabelsAndReplacesRawBytes(t *testing.T) {
	attrs := pcommon.NewMap()
	ident{
		id: "enc\xff:1", name: "Slot\x80", parent: "ctl\xfe",
		extra: []string{hwschema.AttrModel, "M\xc3", hwschema.AttrSerialNumber, "S\xff1"},
	}.put(attrs, hwschema.TypePhysicalDisk)
	want := map[string]string{
		hwschema.AttrID:           "enc�:1",
		hwschema.AttrName:         "Slot�",
		hwschema.AttrParent:       "ctl�",
		hwschema.AttrModel:        "M�",
		hwschema.AttrSerialNumber: "S�1",
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

// hw.vendor and hw.model go through the same extra-attribute call as the serial.
func TestIdentPut_ReplacesRawBytesInVendorAndModelExtras(t *testing.T) {
	attrs := pcommon.NewMap()
	ident{id: "d1", extra: []string{hwschema.AttrVendor, "AT\xffA", hwschema.AttrModel, "ST\xfe40"}}.
		put(attrs, hwschema.TypePhysicalDisk)
	for key, want := range map[string]string{hwschema.AttrVendor: "AT�A", hwschema.AttrModel: "ST�40"} {
		if got, ok := attrs.Get(key); !ok || got.Str() != want {
			t.Errorf("%s = %v, want %q", key, got.AsString(), want)
		}
	}
}

// The controller address comes from sysfs and is not decoded by the ASCII-only
// reader, so a raw byte in it reaches hw.id and hw.parent through ident.put. The
// byte must become U+FFFD and the series must stay, with the same cleaned id on
// every series of the controller.
func TestScrape_RawByteInTheControllerAddressBecomesTheReplacementCharacter(t *testing.T) {
	s, _, _ := newTestScraper(t, testConfig(), labFake(t), controller{HostNo: 0, PCI: "0000:\xff3:00.0"})

	md := s.scrape(context.Background()).metrics

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	const cleaned = "0000:�3:00.0"
	if _, ok := find(md, metricCtrlInfo, hwschema.AttrID, cleaned); !ok {
		t.Error("the controller series is missing under the cleaned address")
	}
	ld, ok := find(md, metricStatus, hwschema.AttrID, cleaned+"/ld0")
	if !ok {
		t.Fatal("the logical drive series is missing under the cleaned address")
	}
	if ld.attrs[hwschema.AttrParent] != cleaned {
		t.Errorf("hw.parent = %q, want %q", ld.attrs[hwschema.AttrParent], cleaned)
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

// A controller and drives that answer with raw bytes in their model, serial,
// vendor and product strings must send no invalid UTF-8, and the
// series must stay.
func TestScrape_RawBytesInControllerAndDriveStringsNeverReachTheExporter(t *testing.T) {
	f := newFake(t)
	f.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{
		device: pciDevice2208, product: "PERC\xff H710P", serial: "CTRL\xfe01", fw: "3.130\x80", memMB: 1024,
		maxLDs: 64, bbu: false, pdPresent: 1, roc: 61, board: 44,
	}))
	f.set(opPDGetList, pdListBuf(pdAddr{DeviceID: 4, EnclID: 32, Slot: 0}))
	f.set(opLDGetList, ldListBuf(0))
	ld := ldSpec{target: 0, name: "da\xc3ta", level: 5, spans: 1, stripeLog2: 7, drives: 1, state: ldStateOptimal, cache: 0x03, blocks: 1 << 21}
	f.setMbox(opLDGetInfo, mailbox(0), ldInfoBuf(ld))
	f.setMbox(opLDGetProp, mailbox(0), ldPropBuf(ld))
	p := pdSpec{id: 4, vendor: "AT\xffA", product: "ST\xfe4000", serial: "ZC\x801", fw: pdOnline, blocks: 1 << 22, encl: 32, slot: 0, temp: 34}
	f.setMbox(opPDGetInfo, mailbox(byte(p.id), byte(p.id>>8)), pdInfoBuf(p))
	s, _, _ := newTestScraper(t, testConfig(), f)

	md := s.scrape(context.Background()).metrics

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	if len(points(md, metricStatus)) == 0 {
		t.Fatal("the raw bytes dropped the hw.status series")
	}
	// The decoder keeps printable ASCII only, so the raw byte is gone before it
	// reaches the attribute; the series stays under the cleaned serial.
	if _, ok := find(md, metricStatus, hwschema.AttrID, "ZC1"); !ok {
		t.Error("the drive series is missing under its cleaned serial")
	}
}
