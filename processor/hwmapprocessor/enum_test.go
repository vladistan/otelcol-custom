// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor

package hwmapprocessor

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// operStatusRule gives 1 for the state 1 and 0 for the rest; the table names the states 1 to 7.
func operStatusRule() Rule {
	return Rule{
		Name:   "oper.status",
		Match:  Match{Metric: "test.oper.status"},
		Target: "hw.network.up",
		Labels: map[string]string{"hw.type": "network", "hw.id": "{if.index}", "hw.name": "{if.name}"},
		EnumGauge: &EnumGauge{
			On:             []string{"1"},
			StateAttribute: "hw.network.oper_state",
			Names: map[string]string{
				"1": "up", "2": "down", "3": "testing", "4": "unknown",
				"5": "dormant", "6": "notPresent", "7": "lowerLayerDown",
			},
		},
	}
}

func TestEnumGauge_EachStateOneToSevenGivesOnePointOneOnlyForUp(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{operStatusRule()}})
	var pts []pt
	for v := int64(1); v <= 7; v++ {
		pts = append(pts, ip(v, "if.index", strconv.FormatInt(v, 10), "if.name", "port"+strconv.FormatInt(v, 10)))
	}

	got := h.run(build(nil, gaugeOf("test.oper.status", "1", pts...)))

	up := named(got, "hw.network.up")
	require.Len(t, up, 7, "one point for each source point")
	names := []string{"up", "down", "testing", "unknown", "dormant", "notPresent", "lowerLayerDown"}
	for i, p := range up {
		assert.Equal(t, names[i], p.attrs["hw.network.oper_state"])
		want := 0.0
		if i == 0 {
			want = 1
		}
		assert.Equal(t, want, p.value, names[i])
		assert.True(t, p.isInt, names[i])
		assert.Equal(t, "1", p.unit)
		assert.Equal(t, pmetric.MetricTypeGauge, p.kind)
		assert.Equal(t, "network", p.attrs["hw.type"])
		assert.Equal(t, strconv.Itoa(i+1), p.attrs["hw.id"])
		assert.Equal(t, "port"+strconv.Itoa(i+1), p.attrs["hw.name"])
	}
	assert.Empty(t, named(got, "test.oper.status"), "the source metric is gone")
	assert.Equal(t, int64(0), h.counter(unmappedStatesName, "", ""), "the seven states are all listed")
}

func TestEnumGauge_ValueOutsideTheTableGivesZeroKeepsTheNumberAndIsCountedAndLoggedOnce(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{operStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.oper.status", "1",
		ip(9, "if.index", "1", "if.name", "a"), ip(9, "if.index", "2", "if.name", "b"), ip(0, "if.index", "3", "if.name", "c"))))

	up := named(got, "hw.network.up")
	require.Len(t, up, 3)
	for _, p := range up {
		assert.Equal(t, 0.0, p.value)
	}
	assert.Equal(t, "9", up[0].attrs["hw.network.oper_state"])
	assert.Equal(t, "9", up[1].attrs["hw.network.oper_state"])
	assert.Equal(t, "0", up[2].attrs["hw.network.oper_state"])
	assert.Equal(t, int64(3), h.counter(unmappedStatesName, "rule", "oper.status"), "counted for each point")
	assert.Equal(t, 2, h.logs.Len(), "one line for 9 and one for 0, not one for each point")
}

func TestEnumGauge_ADoubleThatIsAWholeNumberMatchesTheStateKey(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{operStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.oper.status", "1", dp(1, "if.index", "1"), dp(2, "if.index", "2"))))

	up := named(got, "hw.network.up")
	require.Len(t, up, 2)
	assert.Equal(t, 1.0, up[0].value)
	assert.Equal(t, "up", up[0].attrs["hw.network.oper_state"])
	assert.Equal(t, "down", up[1].attrs["hw.network.oper_state"])
}

func TestEnumGauge_ReadsTheStateFromAnAttribute(t *testing.T) {
	r := Rule{
		Name: "link", Match: Match{Metric: "test.link"}, Target: "hw.network.up",
		EnumGauge: &EnumGauge{Source: "attribute", Attribute: "link.state", On: []string{"up", "UP"}},
	}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.link", "1",
		ip(0, "link.state", "up"), ip(0, "link.state", "UP"), ip(0, "link.state", "down"), ip(0))))

	up := named(got, "hw.network.up")
	require.Len(t, up, 3)
	assert.Equal(t, []float64{1, 1, 0}, []float64{up[0].value, up[1].value, up[2].value})
	assert.Equal(t, "down", up[2].attrs["hw.state"], "the default attribute holds the raw state")
	assert.Equal(t, int64(0), h.counter(unmappedStatesName, "", ""), "no names are configured, so nothing is unlisted")
	assert.Equal(t, int64(1), h.counter(unmappedPointsName, "metric", "test.link"), "a point without the attribute is not mapped")
	assert.Len(t, named(got, "test.link"), 1, "and it passes unchanged")
}

func TestEnumGauge_ASumSourceStillLeavesAGauge(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{operStatusRule()}})
	src := gaugeOf("test.oper.status", "1", ip(1, "if.index", "1"))
	src.sum, src.monotonic = true, true

	got := h.run(build(nil, src))

	up := named(got, "hw.network.up")
	require.Len(t, up, 1)
	assert.Equal(t, pmetric.MetricTypeGauge, up[0].kind)
}

func TestEnumGauge_KeepsTimestampsAndSourceAttributes(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{operStatusRule()}})
	p := ip(2, "if.index", "4", "if.name", "eth4", "extra", "kept")
	p.ts, p.startTS = 2000, 1000

	got := h.run(build(nil, gaugeOf("test.oper.status", "1", p)))

	up := named(got, "hw.network.up")
	require.Len(t, up, 1)
	assert.Equal(t, int64(2000), up[0].ts)
	assert.Equal(t, int64(1000), up[0].startTS)
	assert.Equal(t, "kept", up[0].attrs["extra"])
}

func TestEnumGauge_TheStatusRuleStaysTheSame(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{valueStatusRule(), operStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(2, "id", "x")), gaugeOf("test.oper.status", "1", ip(1, "if.index", "1"))))

	status := named(got, "hw.status")
	require.Len(t, status, 3, "a status rule still gives one point for each state")
	assert.Equal(t, 1.0, byState(status)["degraded"].value)
	assert.Len(t, named(got, "hw.network.up"), 1)
}

func enumRule() Rule { return operStatusRule() }

func TestValidate_EnumGaugeRule(t *testing.T) {
	assert.NoError(t, (&Config{Rules: []Rule{enumRule()}}).Validate())

	cases := map[string]func(*Rule){
		"status and enum gauge together": func(r *Rule) { r.Status = &Status{} },
		"no states that give 1":          func(r *Rule) { r.EnumGauge.On = nil },
		"empty state that gives 1":       func(r *Rule) { r.EnumGauge.On = []string{"1", ""} },
		"on state without a name":        func(r *Rule) { r.EnumGauge.On = []string{"1", "8"} },
		"empty name key":                 func(r *Rule) { r.EnumGauge.Names[""] = "x" },
		"attribute source no key":        func(r *Rule) { r.EnumGauge.Source = "attribute" },
		"unknown source":                 func(r *Rule) { r.EnumGauge.Source = "magic" },
	}
	for name, mutate := range cases {
		r := enumRule()
		mutate(&r)

		err := (&Config{Rules: []Rule{r}}).Validate()

		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `"oper.status"`, name)
	}
}

func TestValidate_EnumGaugeWithNoNamesAcceptsAnyOnState(t *testing.T) {
	r := Rule{
		Name: "link", Match: Match{Metric: "test.link"}, Target: "hw.network.up",
		EnumGauge: &EnumGauge{On: []string{"7"}},
	}

	assert.NoError(t, (&Config{Rules: []Rule{r}}).Validate())
}
