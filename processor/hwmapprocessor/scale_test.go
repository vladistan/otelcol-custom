// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scale that is not a finite number above 0 is refused, and the error names the rule.
func TestScale_OnlyAFiniteNumberAboveZeroLoads(t *testing.T) {
	for name, scale := range map[string]float64{
		"zero": 0, "negative": -0.5, "NaN": math.NaN(), "positive infinity": math.Inf(1), "negative infinity": math.Inf(-1),
	} {
		r := goodRule("a")
		r.Scale = f64(scale)

		err := (&Config{Rules: []Rule{r}}).Validate()

		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `rule "a"`, name)
		assert.Contains(t, err.Error(), "scale", name)
	}
	for _, scale := range []float64{1e-9, 0.001, 1, 1024, 1e9} {
		r := goodRule("a")
		r.Scale = f64(scale)

		assert.NoError(t, (&Config{Rules: []Rule{r}}).Validate(), "%v", scale)
	}
}

// A rule with a scale and no unit keeps the unit of the source; the docs say to set unit when the unit changes.
func TestScale_WithoutAUnitTheSourceUnitStays(t *testing.T) {
	r := Rule{Name: "r", Match: Match{Metric: "m"}, Target: "t.out", Scale: f64(2)}
	h := newHarness(t, &Config{Rules: []Rule{r}})

	got := h.run(build(nil, gaugeOf("m", "mW", ip(3))))

	require.Len(t, got, 1)
	assert.Equal(t, "mW", got[0].unit)
	assert.Equal(t, 6.0, got[0].value)
}
