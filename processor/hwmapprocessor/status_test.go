// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valueStatusRule maps an enum number to ok, degraded or failed; 5 means not present.
func valueStatusRule() Rule {
	return Rule{
		Name:   "env.state",
		Match:  Match{Metric: "test.env.state"},
		Target: "hw.status",
		Status: &Status{
			States: []string{"ok", "degraded", "failed"},
			Map:    map[string]string{"1": "ok", "2": "degraded", "3": "failed", "4": "failed", "5": "drop", "6": "failed"},
		},
	}
}

// textStatusRule maps a disk SMART text. Only GOOD is listed (and NoDisk, which is dropped), so Warning
// and any other text is unlisted and takes the default state.
func textStatusRule(def string) Rule {
	return Rule{
		Name:   "disk.smart",
		Match:  Match{Metric: "test.disk.smart"},
		Target: "hw.status",
		Status: &Status{
			Source:       "attribute",
			Attribute:    "smart",
			States:       []string{"ok", "degraded", "failed"},
			Map:          map[string]string{"GOOD": "ok", "NoDisk": "drop"},
			DefaultState: def,
		},
	}
}

func byState(points []flat) map[string]flat {
	m := map[string]flat{}
	for _, p := range points {
		m[p.attrs["hw.state"]] = p
	}
	return m
}

func TestStatus_DeviceInTheSecondStateGivesThreePointsZeroOneZero(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{valueStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(2, "id", "x"))))

	require.Len(t, got, 3)
	states := byState(got)
	assert.Equal(t, 0.0, states["ok"].value)
	assert.Equal(t, 1.0, states["degraded"].value)
	assert.Equal(t, 0.0, states["failed"].value)
	for _, p := range got {
		assert.Equal(t, "hw.status", p.metric)
		assert.Equal(t, "1", p.unit)
		assert.True(t, p.isInt)
		assert.Equal(t, "x", p.attrs["id"], "source attributes stay")
	}
}

// Every state of the map lands on the right one.
func TestStatus_EveryMappedValueSetsExactlyOneState(t *testing.T) {
	for raw, want := range map[int64]string{1: "ok", 2: "degraded", 3: "failed", 4: "failed", 6: "failed"} {
		h := newHarness(t, &Config{Rules: []Rule{valueStatusRule()}})

		got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(raw))))

		ones := 0
		for state, p := range byState(got) {
			if p.value == 1 {
				ones++
				assert.Equal(t, want, state, "value %d", raw)
			}
		}
		assert.Equal(t, 1, ones, "value %d sets one state", raw)
		assert.Len(t, got, 3, "value %d", raw)
	}
}

// A state list of two gives two points.
func TestStatus_TheStateListSetsTheFanOut(t *testing.T) {
	r := valueStatusRule()
	r.Status.States = []string{"ok", "failed"}
	r.Status.Map = map[string]string{"1": "ok", "3": "failed"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(3))))

	require.Len(t, got, 2)
	assert.Equal(t, 1.0, byState(got)["failed"].value)
	assert.Equal(t, 0.0, byState(got)["ok"].value)
}

func TestStatus_DropEntryGivesNoPointAndNoUnmappedCount(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{valueStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(5, "id", "fan1"), ip(1, "id", "fan2"))))

	require.Len(t, got, 3, "only fan2 gives its three points")
	for _, p := range got {
		assert.Equal(t, "fan2", p.attrs["id"])
	}
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "", ""))
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_states", "", ""))
}

func TestStatus_UnlistedTextTakesTheDefaultStateWithTheRawText(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "Warning", "disk", "d1"))))

	require.Len(t, got, 3)
	states := byState(got)
	assert.Equal(t, 1.0, states["degraded"].value)
	assert.Equal(t, 0.0, states["ok"].value)
	assert.Equal(t, 0.0, states["failed"].value)
	for _, p := range got {
		assert.Equal(t, "Warning", p.attrs["hw.status.raw"])
	}
	assert.Equal(t, int64(1), h.counter("otelcol_hwmap_unmapped_states", "rule", "disk.smart"))
}

// A text of the map keeps its state and carries no raw attribute.
func TestStatus_MappedTextGivesItsStateWithoutTheRawText(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "GOOD"))))

	require.Len(t, got, 3)
	assert.Equal(t, 1.0, byState(got)["ok"].value)
	for _, p := range got {
		_, has := p.attrs["hw.status.raw"]
		assert.False(t, has)
	}
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_states", "", ""))
}

// A drop entry of a rule that reads the state from an attribute gives no point and no count.
func TestStatus_DropEntryOfATextRuleGivesNoPointAndNoCount(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "NoDisk", "disk", "d9"), ip(0, "smart", "GOOD", "disk", "d1"))))

	require.Len(t, got, 3, "only the GOOD disk gives its points")
	for _, p := range got {
		assert.Equal(t, "d1", p.attrs["disk"])
	}
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_states", "", ""))
	assert.Equal(t, int64(0), h.counter("otelcol_hwmap_unmapped_points", "", ""))
}

func TestStatus_NoDefaultLeavesTheUnlistedTextUnchangedCountedAndLoggedOnce(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("")}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(7, "smart", "Odd", "disk", "d1"), ip(8, "smart", "Odd", "disk", "d2"))))

	require.Len(t, got, 2)
	for _, p := range got {
		assert.Equal(t, "test.disk.smart", p.metric)
		assert.Equal(t, "Odd", p.attrs["smart"])
	}
	assert.Equal(t, int64(2), h.counter("otelcol_hwmap_unmapped_states", "rule", "disk.smart"))
	odd := 0
	for _, e := range h.logs.All() {
		if strings.Contains(e.Message, "state") && e.ContextMap()["value"] == "Odd" {
			odd++
		}
	}
	assert.Equal(t, 1, odd, "one log line for the value")
}

// Another value gets its own line; the same value in a later batch does not.
func TestStatus_LogsOncePerValueAcrossBatches(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("")}})

	h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "A"))))
	h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "A"), ip(0, "smart", "B"))))

	assert.Equal(t, 2, h.logs.Len())
}

// The default state also counts the unlisted text.
func TestStatus_DefaultStateCountsTheUnlistedTextToo(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})

	h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "X"), ip(0, "smart", "X"))))

	assert.Equal(t, int64(2), h.counter("otelcol_hwmap_unmapped_states", "rule", "disk.smart"))
	assert.Equal(t, 1, h.logs.Len())
}

// A point with no attribute that holds the text is unmapped, not a state.
func TestStatus_MissingTextAttributeLeavesThePointUnmapped(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "disk", "d1"))))

	require.Len(t, got, 1)
	assert.Equal(t, "test.disk.smart", got[0].metric)
	assert.Equal(t, int64(1), h.counter("otelcol_hwmap_unmapped_points", "metric", "test.disk.smart"))
}

// The state and raw attribute names come from the config.
func TestStatus_AttributeNamesCanBeSet(t *testing.T) {
	r := textStatusRule("degraded")
	r.Status.StateAttribute, r.Status.RawAttribute = "state", "raw"
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", "Odd"))))

	require.Len(t, got, 3)
	for _, p := range got {
		assert.NotEmpty(t, p.attrs["state"])
		assert.Equal(t, "Odd", p.attrs["raw"])
		_, old := p.attrs["hw.state"]
		assert.False(t, old)
	}
}

// A double that holds a whole number reads as that number.
func TestStatus_ADoubleWithAWholeValueMatchesTheIntegerKey(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{valueStatusRule()}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", dp(2.0))))

	assert.Equal(t, 1.0, byState(got)["degraded"].value)
}

// Robustness: invalid UTF-8 and a very long text are made safe before they become an attribute.
func TestStatus_RawTextIsValidUTF8AndBounded(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("degraded")}})
	bad := "bad\xff\xfe" + strings.Repeat("é", 400)

	got := h.run(build(nil, gaugeOf("test.disk.smart", "1", ip(0, "smart", bad))))

	require.Len(t, got, 3)
	raw := got[0].attrs["hw.status.raw"]
	assert.True(t, utf8.ValidString(raw))
	assert.LessOrEqual(t, len(raw), 256+3)
	assert.True(t, strings.HasPrefix(raw, "bad"))
	for _, e := range h.logs.All() {
		for _, v := range e.ContextMap() {
			if s, ok := v.(string); ok {
				assert.True(t, utf8.ValidString(s), "the log holds valid UTF-8")
			}
		}
	}
}

// Robustness: a flood of different texts keeps the table of logged values bounded.
func TestStatus_ManyDistinctTextsDoNotGrowTheLogWithoutBound(t *testing.T) {
	h := newHarness(t, &Config{Rules: []Rule{textStatusRule("")}})
	var pts []pt
	for i := range 5000 {
		pts = append(pts, ip(0, "smart", "v"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+string(rune('a'+(i/676)%26))))
	}

	h.run(build(nil, gaugeOf("test.disk.smart", "1", pts...)))

	assert.LessOrEqual(t, h.logs.Len(), maxLoggedValues+1)
	assert.Equal(t, int64(5000), h.counter("otelcol_hwmap_unmapped_states", "", ""))
}
