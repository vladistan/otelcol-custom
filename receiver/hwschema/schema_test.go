// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"strings"
	"testing"
)

func TestMetricNamesStartWithHW(t *testing.T) {
	for _, spec := range PhysicalDiskMetrics {
		if !strings.HasPrefix(spec.Name, "hw.") {
			t.Errorf("metric %q does not start with hw.", spec.Name)
		}
		if spec.Unit == "" {
			t.Errorf("metric %q has no unit", spec.Name)
		}
		if spec.Description == "" {
			t.Errorf("metric %q has no description", spec.Name)
		}
	}
}

func TestMetricNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range PhysicalDiskMetrics {
		if seen[spec.Name] {
			t.Errorf("metric %q is listed twice", spec.Name)
		}
		seen[spec.Name] = true
	}
}

func TestSpecGivesTheKindOfEachMetric(t *testing.T) {
	want := map[string]Kind{
		MetricStatus:                  Gauge,
		MetricTemperature:             Gauge,
		MetricPhysicalDiskSmart:       Gauge,
		MetricPhysicalDiskEndurance:   Gauge,
		MetricPhysicalDiskSize:        Gauge,
		MetricPhysicalDiskPowerOnTime: Gauge,
		MetricErrors:                  Sum,
		MetricPhysicalDiskIO:          Sum,
		MetricPhysicalDiskPowerCycles: Sum,
	}
	for name, kind := range want {
		spec, ok := LookupMetric(name)
		if !ok {
			t.Errorf("metric %q is not in the table", name)
			continue
		}
		if spec.Kind != kind {
			t.Errorf("metric %q kind = %v, want %v", name, spec.Kind, kind)
		}
	}
}

func TestLookupMetricRejectsAnUnknownName(t *testing.T) {
	if _, ok := LookupMetric("smartctl_device"); ok {
		t.Error("an unknown name was found")
	}
}

func TestWorstStatePicksTheMoreSevere(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{StateOK, StateDegraded, StateDegraded},
		{StateDegraded, StatePredictedFailure, StatePredictedFailure},
		{StatePredictedFailure, StateFailed, StateFailed},
		{StateFailed, StateOK, StateFailed},
		{StateOK, StateOK, StateOK},
		{StateNeedsCleaning, StateOK, StateNeedsCleaning},
	}
	for _, c := range cases {
		if got := WorstState(c.a, c.b); got != c.want {
			t.Errorf("WorstState(%s, %s) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

func TestStatesListEveryValueOnce(t *testing.T) {
	want := []string{StateOK, StateNeedsCleaning, StateDegraded, StatePredictedFailure, StateFailed}
	if len(States) != len(want) {
		t.Fatalf("States = %v, want %d entries", States, len(want))
	}
	for i, s := range want {
		if States[i] != s {
			t.Errorf("States[%d] = %q, want %q", i, States[i], s)
		}
	}
}
