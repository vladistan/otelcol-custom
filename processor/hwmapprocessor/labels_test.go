// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor

package hwmapprocessor

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plain(name, metric, target string) Rule {
	return Rule{Name: name, Match: Match{Metric: metric}, Target: target}
}

func TestIndexNames_EachPointGetsTheNameOfItsIndex(t *testing.T) {
	r := plain("env.temp", "test.env.temp", "hw.temperature")
	r.IndexNames = &IndexNames{Attribute: "env.index", Names: map[string]string{"1": "Sensor A", "2": "Sensor B"}}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.env.temp", "Cel", ip(61, "env.index", "1"), ip(55, "env.index", "2"), ip(40, "env.index", "9"))))

	require.Len(t, got, 3)
	want := map[string]string{"1": "Sensor A", "2": "Sensor B", "9": "9"}
	for _, p := range got {
		assert.Equal(t, p.attrs["env.index"], p.attrs["hw.id"])
		assert.Equal(t, want[p.attrs["env.index"]], p.attrs["hw.name"])
	}
}

// A point with no index attribute gets neither.
func TestIndexNames_APointWithoutTheIndexGetsNoIdentity(t *testing.T) {
	r := plain("env.temp", "test.env.temp", "hw.temperature")
	r.IndexNames = &IndexNames{Attribute: "env.index", Names: map[string]string{"1": "Sensor A"}}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.env.temp", "Cel", ip(61))))

	require.Len(t, got, 1)
	_, hasID := got[0].attrs["hw.id"]
	_, hasName := got[0].attrs["hw.name"]
	assert.False(t, hasID || hasName)
}

// One rule takes the labels from a template, another from a source attribute.
func TestLabels_FromATemplateInOneRuleAndFromASourceAttributeInAnother(t *testing.T) {
	disk := plain("disk.temp", "test.disk.temp", "hw.temperature")
	disk.Labels = map[string]string{"hw.type": "physical_disk", "hw.id": "HDD{disk.slot}", "hw.name": "{disk.descr}"}
	fan := plain("fan.speed", "test.fan.speed", "hw.fan.speed")
	fan.Labels = map[string]string{"hw.type": "fan", "hw.id": "{fan.index}", "hw.name": "{fan.descr}"}
	h := newHarness(t, &Config{Rules: []Rule{disk, fan}})

	got := h.run(build(nil,
		gaugeOf("test.disk.temp", "Cel", ip(37, "disk.slot", "2", "disk.descr", "Drive 2")),
		gaugeOf("test.fan.speed", "rpm", ip(949, "fan.index", "1", "fan.descr", "Fan A")),
	))

	d := named(got, "hw.temperature")[0].attrs
	assert.Equal(t, "physical_disk", d["hw.type"])
	assert.Equal(t, "HDD2", d["hw.id"])
	assert.Equal(t, "Drive 2", d["hw.name"])
	f := named(got, "hw.fan.speed")[0].attrs
	assert.Equal(t, "fan", f["hw.type"])
	assert.Equal(t, "1", f["hw.id"])
	assert.Equal(t, "Fan A", f["hw.name"])
}

// A template that names a missing attribute leaves that label off; the other labels stay.
func TestLabels_AMissingSourceAttributeLeavesThatLabelOff(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.Labels = map[string]string{"hw.type": "fan", "hw.name": "{absent}"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "1", ip(1, "hw.name", "old"))))

	assert.Equal(t, "fan", got[0].attrs["hw.type"])
	assert.Equal(t, "old", got[0].attrs["hw.name"], "the source attribute of that name stays")
}

// A label overrides a source attribute of the same key; an index name is overridden by a label.
func TestLabels_OverrideTheSourceAttributeAndTheIndexName(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.IndexNames = &IndexNames{Attribute: "i", Names: map[string]string{"1": "from index"}}
	r.Labels = map[string]string{"hw.name": "from label"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "1", ip(1, "i", "1", "hw.name", "source"))))

	assert.Equal(t, "from label", got[0].attrs["hw.name"])
	assert.Equal(t, "1", got[0].attrs["hw.id"])
}

// The labels reach every point of a status fan-out.
func TestLabels_EveryStatusPointCarriesThem(t *testing.T) {
	r := valueStatusRule()
	r.Labels = map[string]string{"hw.type": "fan", "hw.id": "{id}"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(1, "id", "f1"))))

	require.Len(t, got, 3)
	for _, p := range got {
		assert.Equal(t, "fan", p.attrs["hw.type"])
		assert.Equal(t, "f1", p.attrs["hw.id"])
	}
}

// Robustness: a rendered label is valid UTF-8 and bounded.
func TestLabels_RenderedTextIsValidUTF8(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.Labels = map[string]string{"hw.name": "<{n}>"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "1", ip(1, "n", "a\xff-b"))))

	assert.True(t, utf8.ValidString(got[0].attrs["hw.name"]))
	assert.Equal(t, "<a�-b>", got[0].attrs["hw.name"])
}

func TestConstants_ThreeOfThemLandOnEveryPoint(t *testing.T) {
	r := valueStatusRule()
	r.Constants = map[string]string{"hw.limit_type": "high.critical", "poe.measure": "allocated", "iscsi.lun": "true"}
	plainRule := plain("p", "test.other", "hw.power")
	plainRule.Constants = r.Constants
	h := newHarness(t, &Config{Rules: []Rule{r, plainRule}})

	got := h.run(build(nil, gaugeOf("test.env.state", "1", ip(1)), gaugeOf("test.other", "W", ip(5))))

	require.Len(t, got, 4)
	for _, p := range got {
		assert.Equal(t, "high.critical", p.attrs["hw.limit_type"], p.metric)
		assert.Equal(t, "allocated", p.attrs["poe.measure"], p.metric)
		assert.Equal(t, "true", p.attrs["iscsi.lun"], p.metric)
	}
}

// A constant wins over a label and a source attribute of the same key.
func TestConstants_WinOverLabelsAndSourceAttributes(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.Labels = map[string]string{"k": "label"}
	r.Constants = map[string]string{"k": "constant"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "1", ip(1, "k", "source"))))

	assert.Equal(t, "constant", got[0].attrs["k"])
}

// A constant is literal text: a brace in it is not a template.
func TestConstants_AreLiteral(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.Constants = map[string]string{"k": "{not a template}"}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "1", ip(1))))

	assert.Equal(t, "{not a template}", got[0].attrs["k"])
}

func TestResource_VendorAndModelLandOnTheResourceOfTheEmittedPoints(t *testing.T) {
	r := plain("r", "m", "t.out")
	r.Resource = map[string]string{"hw.vendor": "Example", "hw.model": "X-1"}
	h := newHarness(t, &Config{Rules: []Rule{r}})
	md := build(map[string]string{"host.name": "a"}, gaugeOf("m", "1", ip(1)))
	other := build(map[string]string{"host.name": "b"}, gaugeOf("unrelated", "1", ip(1)))
	other.ResourceMetrics().At(0).CopyTo(md.ResourceMetrics().AppendEmpty())

	h.e.process(t.Context(), md)

	got := flatten(md)
	a := named(got, "t.out")[0].res
	assert.Equal(t, "Example", a["hw.vendor"])
	assert.Equal(t, "X-1", a["hw.model"])
	assert.Equal(t, "a", a["host.name"])
	b := named(got, "unrelated")[0].res
	_, has := b["hw.vendor"]
	assert.False(t, has, "a resource with no emitted point is left alone")
}

// A status point that is dropped adds no resource attribute.
func TestResource_ADroppedPointAddsNothing(t *testing.T) {
	r := valueStatusRule()
	r.Resource = map[string]string{"hw.vendor": "Example"}
	h := newHarness(t, &Config{Rules: []Rule{r}})
	md := build(map[string]string{"host.name": "a"}, gaugeOf("test.env.state", "1", ip(5)))

	h.e.process(t.Context(), md)

	assert.Empty(t, flatten(md))
	_, has := md.ResourceMetrics().At(0).Resource().Attributes().Get("hw.vendor")
	assert.False(t, has)
}
