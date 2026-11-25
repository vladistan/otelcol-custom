// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// The interface speed is polled in Mbit/s; the rule in the sample scrape config turns it into bytes per
// second. Index and name are the attributes the sample declares; every value is a documentation example.

func speedPoint(v int64, index, name string) pt {
	if name == "" {
		return ip(v, "interface.index", index)
	}
	return ip(v, "interface.index", index, "interface", name)
}

func TestBandwidth_ValidateAcceptsBytesPerSecondAndRefusesAnUnknownUnit(t *testing.T) {
	good := goodRule("a")
	good.Unit = "By/s"
	assert.NoError(t, (&Config{Rules: []Rule{good}}).Validate())

	for _, unit := range []string{"bit/s", "By/min", "by/s", "By/S", "Mbit/s"} {
		r := goodRule("a")
		r.Unit = unit

		err := (&Config{Rules: []Rule{r}}).Validate()

		if assert.Error(t, err, unit) {
			assert.Contains(t, err.Error(), `rule "a"`, unit)
		}
	}
}

func TestBandwidth_SpeedFixturesGiveBytesPerSecondAsAGauge(t *testing.T) {
	tests := []struct {
		name string
		mbit int64
		want float64
	}{
		{"one gigabit", 1000, 125000000},
		{"ten gigabit", 10000, 1250000000},
		{"no speed gives zero", 0, 0},
		{"a hundred megabit", 100, 12500000},
		{"the largest value of the source type is emitted as read", 4294967295, 536870911875000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, loadSample(t))

			got := h.run(build(nil, gaugeOf("snmp.interface.high_speed", "Mbit/s", speedPoint(tt.mbit, "10", "Gi1/0/1"))))

			pts := named(got, "hw.network.bandwidth.limit")
			require.Len(t, pts, 1)
			p := pts[0]
			assert.Equal(t, tt.want, p.value)
			assert.Equal(t, "By/s", p.unit)
			assert.Equal(t, pmetric.MetricTypeGauge, p.kind)
			assert.Equal(t, "network", p.attrs["hw.type"])
			assert.Equal(t, "10", p.attrs["hw.id"])
			assert.Equal(t, "Gi1/0/1", p.attrs["hw.name"])
			assert.Empty(t, named(got, "snmp.interface.high_speed"), "the source name stops")
		})
	}
}

func TestBandwidth_AnInterfaceWithoutANameLeavesHwNameUnset(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil, gaugeOf("snmp.interface.high_speed", "Mbit/s", speedPoint(1000, "11", ""))))

	pts := named(got, "hw.network.bandwidth.limit")
	require.Len(t, pts, 1)
	assert.Equal(t, 125000000.0, pts[0].value)
	assert.Equal(t, "11", pts[0].attrs["hw.id"])
	assert.NotContains(t, pts[0].attrs, "hw.name")
}

func TestBandwidth_EveryInterfaceOfOneReadingGivesItsOwnPoint(t *testing.T) {
	h := newHarness(t, loadSample(t))

	got := h.run(build(nil, gaugeOf("snmp.interface.high_speed", "Mbit/s",
		speedPoint(1000, "10", "Gi1/0/1"), speedPoint(10000, "49", "Te1/1/1"), speedPoint(0, "50", "Vl1"))))

	byID := map[string]float64{}
	for _, p := range named(got, "hw.network.bandwidth.limit") {
		byID[p.attrs["hw.id"]] = p.value
	}
	assert.Equal(t, map[string]float64{"10": 125000000, "49": 1250000000, "50": 0}, byID)
}
