// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// point is one data point flattened for assertions.
type point struct {
	metric string
	unit   string
	value  float64
	attrs  map[string]string
}

func pointsOf(md pmetric.Metrics) []point {
	var out []point
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				dps := m.Gauge().DataPoints()
				for n := 0; n < dps.Len(); n++ {
					dp := dps.At(n)
					p := point{metric: m.Name(), unit: m.Unit(), attrs: map[string]string{}}
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						p.value = float64(dp.IntValue())
					} else {
						p.value = dp.DoubleValue()
					}
					for key, v := range dp.Attributes().AsRaw() {
						p.attrs[key] = v.(string)
					}
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func named(points []point, metric string) []point {
	var out []point
	for _, p := range points {
		if p.metric == metric {
			out = append(out, p)
		}
	}
	return out
}

func attrsOf(t *testing.T, attrs ...wireAttr) map[string][]value {
	t.Helper()
	m, err := decodeMessage(encodeResponse(0, attrs...))
	require.NoError(t, err)
	got, _ := m.printerAttributes()
	return got
}

func scrapeOf(t *testing.T, attrs ...wireAttr) []point {
	t.Helper()
	return pointsOf(printerMetrics("lab-a", attrsOf(t, attrs...), time.Unix(1000, 0)))
}

func TestStateIdleGivesOnePointWithTheStateName(t *testing.T) {
	got := named(scrapeOf(t, healthyAttrs()...), "printer.ipp.state")

	require.Len(t, got, 1)
	assert.Equal(t, 1.0, got[0].value)
	assert.Equal(t, "idle", got[0].attrs["printer.ipp.state"])
}

func TestStateNames(t *testing.T) {
	for enum, want := range map[int32]string{3: "idle", 4: "processing", 5: "stopped"} {
		got := named(scrapeOf(t, enumAttr("printer-state", enum)), "printer.ipp.state")

		require.Len(t, got, 1)
		assert.Equal(t, want, got[0].attrs["printer.ipp.state"])
	}
}

func TestMissingStateGivesNoStateSeries(t *testing.T) {
	got := scrapeOf(t, boolAttr("printer-is-accepting-jobs", true))

	assert.Empty(t, named(got, "printer.ipp.state"))
	assert.Equal(t, 1.0, named(got, "printer.ipp.reachable")[0].value)
}

func TestReasonsSplitKeywordAndSeverity(t *testing.T) {
	got := named(scrapeOf(t, keywordAttr("printer-state-reasons",
		"media-jam-error", "toner-low-report", "marker-supply-low-warning", "paused")), "printer.ipp.state_reason")

	require.Len(t, got, 4)
	want := []struct{ reason, severity string }{
		{"media-jam", "error"}, {"toner-low", "report"}, {"marker-supply-low", "warning"}, {"paused", "error"},
	}
	for i, w := range want {
		assert.Equal(t, 1.0, got[i].value)
		assert.Equal(t, w.reason, got[i].attrs["printer.ipp.reason"])
		assert.Equal(t, w.severity, got[i].attrs["printer.ipp.reason.severity"])
	}
}

func TestReasonNoneGivesNoPoint(t *testing.T) {
	got := scrapeOf(t, keywordAttr("printer-state-reasons", "none"))

	assert.Empty(t, named(got, "printer.ipp.state_reason"))
}

func TestMarkerLevelsAreRatiosWithNameAndType(t *testing.T) {
	got := named(scrapeOf(t, healthyAttrs()...), "printer.ipp.marker.level")

	require.Len(t, got, 4)
	assert.Equal(t, []float64{0.9, 0.9, 0.9, 1.0}, []float64{got[0].value, got[1].value, got[2].value, got[3].value})
	assert.Equal(t, "Black", got[0].attrs["printer.ipp.marker.name"])
	assert.Equal(t, "toner", got[0].attrs["printer.ipp.marker.type"])
	assert.Equal(t, "Yellow", got[3].attrs["printer.ipp.marker.name"])
	assert.Equal(t, "1", got[0].unit)
}

func TestNegativeMarkerLevelGivesNoPoint(t *testing.T) {
	for _, level := range []int32{-1, -2, -3} {
		got := scrapeOf(t,
			intsAttr("marker-levels", 50, level),
			namesAttr("marker-names", "Black", "Cyan"),
			keywordAttr("marker-types", "toner", "toner"))

		points := named(got, "printer.ipp.marker.level")
		require.Len(t, points, 1)
		assert.Equal(t, "Black", points[0].attrs["printer.ipp.marker.name"])
	}
}

func TestMarkerAttributesOnlyWhereTheIndexExists(t *testing.T) {
	got := named(scrapeOf(t,
		intsAttr("marker-levels", 10, 20, 30),
		namesAttr("marker-names", "Black"),
		keywordAttr("marker-types", "toner", "toner")), "printer.ipp.marker.level")

	require.Len(t, got, 3)
	assert.Equal(t, "Black", got[0].attrs["printer.ipp.marker.name"])
	assert.NotContains(t, got[1].attrs, "printer.ipp.marker.name")
	assert.Equal(t, "toner", got[1].attrs["printer.ipp.marker.type"])
	assert.NotContains(t, got[2].attrs, "printer.ipp.marker.type")
}

func TestAcceptingJobsAndQueuedJobs(t *testing.T) {
	stuck := scrapeOf(t, boolAttr("printer-is-accepting-jobs", false), intAttr("queued-job-count", 3))
	fine := scrapeOf(t, boolAttr("printer-is-accepting-jobs", true), intAttr("queued-job-count", 0))

	assert.Equal(t, 0.0, named(stuck, "printer.ipp.accepting_jobs")[0].value)
	assert.Equal(t, 3.0, named(stuck, "printer.ipp.queued_jobs")[0].value)
	assert.Equal(t, 1.0, named(fine, "printer.ipp.accepting_jobs")[0].value)
	assert.Equal(t, 0.0, named(fine, "printer.ipp.queued_jobs")[0].value)
	assert.Equal(t, "{job}", named(fine, "printer.ipp.queued_jobs")[0].unit)
}

func TestMissingAcceptingAndQueuedGiveNoSeriesAndReachableStaysOne(t *testing.T) {
	got := scrapeOf(t, enumAttr("printer-state", 3))

	assert.Empty(t, named(got, "printer.ipp.accepting_jobs"))
	assert.Empty(t, named(got, "printer.ipp.queued_jobs"))
	assert.Equal(t, 1.0, named(got, "printer.ipp.reachable")[0].value)
}

func TestEverySeriesCarriesTheHardwareAttributes(t *testing.T) {
	got := scrapeOf(t, append(healthyAttrs(), keywordAttr("printer-state-reasons", "toner-low-report"))...)

	require.NotEmpty(t, got)
	for _, p := range got {
		assert.Equal(t, "printer", p.attrs["hw.type"], p.metric)
		assert.Equal(t, "lab-a", p.attrs["hw.id"], p.metric)
		assert.Equal(t, "lab-a", p.attrs["hw.name"], p.metric)
	}
}

func TestFailedPollEmitsOnlyReachableZero(t *testing.T) {
	got := pointsOf(failedMetrics("lab-a", time.Unix(1000, 0)))

	require.Len(t, got, 1)
	assert.Equal(t, "printer.ipp.reachable", got[0].metric)
	assert.Equal(t, 0.0, got[0].value)
	assert.Equal(t, "printer", got[0].attrs["hw.type"])
	assert.Equal(t, "lab-a", got[0].attrs["hw.name"])
}
