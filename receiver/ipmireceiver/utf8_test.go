// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

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

// A sensor name read from the SDR may hold raw bytes. No attribute that
// reaches the exporter may be invalid UTF-8, and the point is kept.
func TestRawBytesInSensorNamesNeverReachTheExporter(t *testing.T) {
	clock := newClock()
	board := newBoard(clock,
		sdrRecord{number: 1, name: "CPU\xff Temp", kind: kindTemperature},
		sdrRecord{number: 2, name: "FAN\xfe1", kind: kindFan},
		sdrRecord{number: 3, name: "12V\x80", kind: kindVoltage},
	)
	board.readings[1] = sensorReading{value: 40}
	board.readings[2] = sensorReading{value: 3000}
	board.readings[3] = sensorReading{value: 12}
	r := newRig(t, board, clock, nil)

	md := r.scrape(t)

	if bad := invalidAttributes(md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	dps := dataPoints(md, "hw.temperature")
	if dps.Len() != 1 {
		t.Fatalf("the temperature point was dropped: %d points", dps.Len())
	}
	name, _ := dps.At(0).Attributes().Get("hw.name")
	if name.Str() != "CPU� Temp" {
		t.Errorf("hw.name = %q, want the raw byte replaced by U+FFFD", name.Str())
	}
}
