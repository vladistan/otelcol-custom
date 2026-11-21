package collect

import (
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
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
			m := ms.At(k)
			var dps pmetric.NumberDataPointSlice
			if m.Type() == pmetric.MetricTypeGauge {
				dps = m.Gauge().DataPoints()
			} else {
				dps = m.Sum().DataPoints()
			}
			for d := 0; d < dps.Len(); d++ {
				check(m.Name(), dps.At(d).Attributes())
			}
		}
	}
	return bad
}

// NVML strings come from the driver as raw bytes. A raw byte in the name,
// model, UUID or driver version must not reach the exporter.
func TestRawBytesInDeviceStringsNeverReachTheExporter(t *testing.T) {
	id := port.DeviceIdentity{UUID: "GPU-\xff1234", Model: "Tesla\xfe A1", Name: "NVIDIA\x80 A1", DriverVersion: "535.\xc3"}
	b := newBatch(t0, t0)
	b.addInt(semconv.MetricMemoryLimit, id, 16)
	b.addDouble(semconv.MetricUtilization, id, 0.5)
	b.addTemperature(id, 40)

	if bad := invalidAttributes(b.md); len(bad) > 0 {
		t.Fatalf("invalid UTF-8 reached the exporter in: %v", bad)
	}
	dp := b.md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	if got, _ := dp.Attributes().Get(semconv.AttrName); got.Str() != "NVIDIA� A1" {
		t.Errorf("hw.name = %q, want the raw byte replaced by U+FFFD", got.Str())
	}
}

func TestValidUTF8ReplacesEachInvalidByteAndKeepsValidText(t *testing.T) {
	cases := map[string]string{
		"NVIDIA A1":    "NVIDIA A1",
		"GPU-\xff\xfe": "GPU-��",
		"":             "",
		"é\xc3":        "é�",
	}
	for in, want := range cases {
		if got := validUTF8(in); got != want {
			t.Errorf("validUTF8(%q) = %q, want %q", in, got, want)
		}
	}
}
