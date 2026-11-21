// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor milli

package hwmapprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func tempRule(attrs map[string]string) Rule {
	return Rule{
		Name:   "temp",
		Match:  Match{Metric: "test.sensor.milli", Attributes: attrs},
		Target: "hw.temperature",
		Unit:   "Cel",
		Scale:  f64(0.001),
	}
}

func TestMatch_ThroughTheConfiguredAttributeKeys(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(map[string]string{"sensor.kind": "temp"})}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel",
		ip(45000, "sensor.kind", "temp"),
		ip(7000, "sensor.other", "temp"),
	)))

	require.Len(t, named(got, "hw.temperature"), 1)
	assert.InDelta(t, 45.0, named(got, "hw.temperature")[0].value, 1e-9)
	left := named(got, "test.sensor.milli")
	require.Len(t, left, 1, "the point without the key stays")
	assert.Equal(t, 7000.0, left[0].value)
}

func TestMatch_AnotherConfigWithOtherKeyNamesMatchesTheRewrittenFixture(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(map[string]string{"k": "t"})}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel", ip(45000, "k", "t"))))

	require.Len(t, named(got, "hw.temperature"), 1)
	assert.Empty(t, named(got, "test.sensor.milli"))
}

// A rule with an empty attribute value needs the key only.
func TestMatch_EmptyValueMeansTheKeyMustBePresent(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(map[string]string{"sensor.kind": ""})}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel", ip(1000, "sensor.kind", "anything"), ip(2000))))

	assert.Len(t, named(got, "hw.temperature"), 1)
	assert.Len(t, named(got, "test.sensor.milli"), 1)
}

func TestRename_NoPointKeepsTheSourceName(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(nil)}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel", ip(1000), ip(2000))))

	assert.Len(t, named(got, "hw.temperature"), 2)
	assert.Empty(t, named(got, "test.sensor.milli"))
}

// The first rule of a metric that matches wins.
func TestRename_FirstMatchingRuleWins(t *testing.T) {
	a := tempRule(map[string]string{"k": "a"})
	b := tempRule(nil)
	b.Name, b.Target = "any", "hw.other"
	h := newHarness(t, &Config{Rules: []Rule{a, b}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel", ip(1000, "k", "a"), ip(2000, "k", "z"))))

	assert.Len(t, named(got, "hw.temperature"), 1)
	assert.Len(t, named(got, "hw.other"), 1)
}

func TestScale_TableOfTheFactorsInUse(t *testing.T) {
	cases := []struct {
		name  string
		in    pt
		scale float64
		unit  string
		want  float64
	}{
		{"millidegrees to Cel", ip(45000), 0.001, "Cel", 45},
		{"milliwatts to W", ip(1500), 0.001, "W", 1.5},
		{"KiBy to By", ip(2), 1024, "By", 2048},
		{"percent to ratio", ip(50), 0.01, "1", 0.5},
		{"tenths of a watt to W", ip(1250), 0.1, "W", 125},
	}
	for _, c := range cases {
		r := Rule{Name: "r", Match: Match{Metric: "m"}, Target: "t.out", Unit: c.unit, Scale: f64(c.scale)}
		h := newHarness(t, &Config{Rules: []Rule{r}})

		got := h.run(build(nil, gaugeOf("m", "x", c.in)))

		require.Len(t, got, 1, c.name)
		assert.InDelta(t, c.want, got[0].value, 1e-9, c.name)
		assert.Equal(t, c.unit, got[0].unit, c.name)
	}
}

// No scale keeps an integer an integer.
func TestScale_NoFactorKeepsTheIntegerAndTheUnit(t *testing.T) {
	r := Rule{Name: "r", Match: Match{Metric: "m"}, Target: "hw.fan.speed"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "rpm", ip(949))))

	require.Len(t, got, 1)
	assert.True(t, got[0].isInt)
	assert.Equal(t, 949.0, got[0].value)
	assert.Equal(t, "rpm", got[0].unit)
}

func TestDropWhenValue_DropsOnlyThePointEqualToTheNumber(t *testing.T) {
	r := tempRule(nil)
	r.Scale = nil
	r.DropWhenValue = f64(0)
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "Cel", ip(0, "i", "1"), ip(90, "i", "2"), dp(0.5, "i", "3"))))

	require.Len(t, got, 2)
	for _, p := range got {
		assert.Equal(t, "hw.temperature", p.metric)
		assert.NotEqual(t, "1", p.attrs["i"], "the point equal to 0 is gone")
	}
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "", ""), "a dropped point is not unmapped")
}

func TestUnmapped_APointNoRuleMatchesPassesAndIsCounted(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(map[string]string{"sensor.kind": "temp"})}})

	got := h.run(build(nil,
		gaugeOf("other.metric", "1", ip(3), ip(4)),
		gaugeOf("test.sensor.milli", "mCel", ip(9000, "sensor.kind", "fan")),
	))

	assert.Len(t, named(got, "other.metric"), 2)
	assert.Len(t, named(got, "test.sensor.milli"), 1)
	assert.Equal(t, int64(2), h.counter("otelcol_hwmap_unmapped_points", "metric", "other.metric"))
	assert.Equal(t, int64(1), h.counter("otelcol_hwmap_unmapped_points", "metric", "test.sensor.milli"))
}

// The metric name is a counter attribute, so a name that is not valid UTF-8 is made valid first.
func TestUnmapped_AMetricNameThatIsNotUTF8GoesToTheCounterCleaned(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(nil)}})

	got := h.run(build(nil, gaugeOf("bad\xff-name", "1", ip(1))))

	assert.Len(t, got, 1, "the point passes unchanged")
	assert.Equal(t, int64(1), h.counter("otelcol_hwmap_unmapped_points", "metric", "bad�-name"))
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "metric", "bad\xff-name"))
}

func TestType_AGaugeStaysAGaugeAndASumStaysASumWithItsTemporality(t *testing.T) {
	r := Rule{Name: "r", Match: Match{Metric: "m"}, Target: "t.out"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	gauge := h.run(build(nil, gaugeOf("m", "1", ip(1))))
	delta := h.run(build(nil, spec{name: "m", unit: "1", sum: true, delta: true, monotonic: true, pts: []pt{ip(1)}}))
	cumulative := h.run(build(nil, spec{name: "m", unit: "1", sum: true, pts: []pt{ip(1)}}))

	assert.Equal(t, pmetric.MetricTypeGauge, gauge[0].kind)
	assert.Equal(t, pmetric.MetricTypeSum, delta[0].kind)
	assert.Equal(t, pmetric.AggregationTemporalityDelta, delta[0].temporality)
	assert.True(t, delta[0].monotonic)
	assert.Equal(t, pmetric.AggregationTemporalityCumulative, cumulative[0].temporality)
	assert.False(t, cumulative[0].monotonic)
}

func TestTimestamps_BothComeThroughUnchanged(t *testing.T) {
	r := tempRule(nil)
	h := newHarness(t, &Config{Rules: []Rule{r}})
	in := ip(45000)
	in.ts, in.startTS = 1700000000123456789, 1700000000000000001

	got := h.run(build(nil, gaugeOf("test.sensor.milli", "mCel", in)))

	require.Len(t, got, 1)
	assert.Equal(t, in.ts, got[0].ts)
	assert.Equal(t, in.startTS, got[0].startTS)
}

func TestOff_NoRulesLeavesEveryPointUnchangedAndUncounted(t *testing.T) {
	h := newHarness(t, &Config{})
	before := flatten(build(nil, gaugeOf("m", "1", ip(1, "a", "b"))))

	got := h.run(build(nil, gaugeOf("m", "1", ip(1, "a", "b"))))

	assert.Equal(t, before, got)
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "", ""))
}

// A metric type the rules cannot map passes unchanged.
func TestOther_AHistogramPassesUnchangedAndIsCounted(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{{Name: "r", Match: Match{Metric: "m"}, Target: "t.out"}}})
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("m")
	m.SetEmptyHistogram().DataPoints().AppendEmpty()

	h.e.process(t.Context(), md)

	assert.Equal(t, "m", md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Name())
	assert.Equal(t, int64(1), h.counter("otelcol_hwmap_unmapped_points", "metric", "m"))
}

// A scope whose metrics are all consumed leaves no empty metric behind.
func TestRename_ASourceMetricWithNoPointLeftIsRemoved(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{tempRule(nil)}})
	md := build(nil, gaugeOf("test.sensor.milli", "mCel", ip(1000)), gaugeOf("keep.me", "1"))

	h.e.process(t.Context(), md)

	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	var names []string
	for i := 0; i < ms.Len(); i++ {
		names = append(names, ms.At(i).Name())
	}
	assert.ElementsMatch(t, []string{"keep.me", "hw.temperature"}, names, "an input metric that was empty from the start stays")
}
