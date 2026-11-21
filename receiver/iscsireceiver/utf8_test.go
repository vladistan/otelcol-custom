// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// invalidAttributes lists the attributes of every data point whose key or
// value is not valid UTF-8.
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
		ms := rms.At(i).ScopeMetrics().At(0).Metrics()
		for k := 0; k < ms.Len(); k++ {
			dps := ms.At(k).Gauge().DataPoints()
			for d := 0; d < dps.Len(); d++ {
				check(ms.At(k).Name(), dps.At(d).Attributes())
			}
		}
	}
	return bad
}

// Target names, portals and device names come from sysfs. A raw byte in one of
// them must not reach the exporter, and the series are kept.
func TestRawBytesInTargetAndDeviceNeverReachTheExporter(t *testing.T) {
	sessions := []session{{id: 1, target: "iqn.\xff-a", tpgt: "1", state: "LOGGED_IN", portal: "192.0.2.10:3260",
		connections: []connection{{portal: "192.0.2.\xfe10:3260", state: "LOGGED_IN"}}}}
	src := &fakeBoth{fakeSessions: fakeSessions{list: sessions}, lunList: []lun{
		{session: 1, id: 3, device: "sd\x80b", holder: "dm-\xc30", state: "running", sizeBytes: 1 << 30, hasSize: true},
	}}

	md := newLUNScraper(zap.NewNop(), src, 10).scrape(time.Unix(1700000000, 0))

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	status := metricNamed(t, md, "hw.status").Gauge().DataPoints().At(0)
	if name, _ := status.Attributes().Get("hw.name"); name.Str() != "sd�b" {
		t.Errorf("hw.name = %q, want the raw byte replaced by U+FFFD", name.Str())
	}
}
