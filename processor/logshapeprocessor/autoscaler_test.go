// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// autoscalerRow is a cluster autoscaler status row whose node count and target are the given JSON values.
func autoscalerRow(count, target string) string {
	return fmt.Sprintf(`{"status":{"autoscaledNodesCount":%s,"autoscaledNodesTarget":%s,"clusterWide":{"health":{"status":"Healthy"}}}}`, count, target)
}

func autoscalerOnly(t *testing.T) *harness {
	t.Helper()
	cfg := defaultConfig()
	cfg.Branches = []string{"autoscaler"}
	return newHarness(t, cfg)
}

// goldenDraw is a deterministic, evenly spread source of numbers in [0, 1).
func goldenDraw() func() float64 {
	i := 0
	return func() float64 {
		i++
		_, frac := math.Modf(float64(i) * 0.6180339887498949)
		return frac
	}
}

func TestAutoscaler_StatusRow_GivesTheBodyAndIntegerAttributes(t *testing.T) {
	h := newHarness(t, defaultConfig())
	raw := autoscalerRow("3", "5")

	lr, _ := h.run(input{body: raw})

	w4Body(t, lr, "autoscaler nodes 3/5")
	a := lr.Attributes()
	attrInt(t, a, "vstan.autoscaler.nodes", 3)
	attrInt(t, a, "vstan.autoscaler.target", 5)
	if got := attrStr(t, a, "log.record.original"); got != raw {
		t.Errorf("log.record.original = %q, want the raw row", got)
	}
	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Errorf("unmatched = %v", got)
	}
}

func TestAutoscaler_RowWithoutBothFields_IsNotClaimed(t *testing.T) {
	for _, tt := range []struct{ name, body string }{
		{"no target", `{"status":{"autoscaledNodesCount":3}}`},
		{"no count", `{"status":{"autoscaledNodesTarget":5}}`},
		{"no status object", `{"autoscaledNodesCount":3,"autoscaledNodesTarget":5}`},
		{"status is text", `{"status":"autoscaledNodesCount autoscaledNodesTarget"}`},
		{"other row", `{"status":{"clusterWide":{"health":{"status":"Healthy"}}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := autoscalerOnly(t)

			lr, _ := h.run(input{body: tt.body})

			w4Untouched(t, lr, tt.body, map[string]any{})
			if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
				t.Errorf("parse_failed = %v, want none for a row that is not claimed", got)
			}
		})
	}
}

func TestAutoscaler_NonIntegerCount_LeavesTheRecordAndIsCounted(t *testing.T) {
	for _, tt := range []struct{ name, count, target string }{
		{"fraction", "3.5", "5"},
		{"text", `"3"`, "5"},
		{"bool", "true", "5"},
		{"null", "null", "5"},
		{"exponent", "1e3", "5"},
		{"beyond int64", "99999999999999999999", "5"},
		{"text target", "3", `"5"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := autoscalerOnly(t)
			body := autoscalerRow(tt.count, tt.target)

			lr, _ := h.run(input{body: body})

			w4Untouched(t, lr, body, map[string]any{})
			w4Failed(t, h, "autoscaler")
		})
	}
}

func TestAutoscaler_RowsWithUnequalCountAndTarget_AreAllKept(t *testing.T) {
	cfg := defaultConfig()
	cfg.Autoscaler.KeepRatio = 0
	h := newHarness(t, cfg)
	h.proc.draw = func() float64 { return 0.99 }
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = autoscalerRow(fmt.Sprint(i%7), fmt.Sprint(i%7+1))
	}

	out := flatten(h.runBatch(batchOf(lines)))

	if len(out) != len(lines) {
		t.Fatalf("%d rows out, %d in", len(out), len(lines))
	}
	if got := h.counter(sampledOutMetric, "format"); len(got) != 0 {
		t.Errorf("sampled_out = %v, want none", got)
	}
}

func TestAutoscaler_DefaultKeepRatio_IsAFifth(t *testing.T) {
	if got := defaultConfig().Autoscaler.KeepRatio; got != 0.2 {
		t.Fatalf("default keep ratio = %v, want 0.2", got)
	}
}

func TestAutoscaler_EqualRows_AreSampledAtTheDefaultRatio(t *testing.T) {
	h := newHarness(t, defaultConfig())
	h.proc.draw = goldenDraw()
	const rows = 10000
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = autoscalerRow("4", "4")
	}

	out := flatten(h.runBatch(batchOf(lines)))

	kept := len(out)
	if kept < rows*18/100 || kept > rows*22/100 {
		t.Fatalf("kept %d of %d equal rows, want 18%% to 22%%", kept, rows)
	}
	for _, r := range out {
		if r.body != "autoscaler nodes 4/4" {
			t.Fatalf("a kept row has the body %q", r.body)
		}
	}
}

func TestAutoscaler_SampledOutCount_EqualsTheRowsMissingFromTheOutput(t *testing.T) {
	cfg := defaultConfig()
	cfg.Autoscaler.KeepRatio = 0.5
	h := newHarness(t, cfg)
	n := 0
	h.proc.draw = func() float64 { n++; return float64(n%2) * 0.9 } // keeps every second equal row
	var lines []string
	for i := range 300 {
		lines = append(lines, autoscalerRow("2", "2"))
		if i < 200 {
			lines = append(lines, autoscalerRow("1", "2"))
		}
	}

	out := flatten(h.runBatch(batchOf(lines)))

	dropped := h.counter(sampledOutMetric, "format")["autoscaler"]
	if dropped == 0 || int64(len(out))+dropped != int64(len(lines)) {
		t.Fatalf("%d rows out + %d sampled out, want %d in", len(out), dropped, len(lines))
	}
	if dropped != 150 {
		t.Errorf("sampled out %d, want 150 of the 300 equal rows", dropped)
	}
}

func TestAutoscaler_KeepRatioZeroAndOne_DropAndKeepEveryEqualRow(t *testing.T) {
	for _, tt := range []struct {
		ratio float64
		kept  int
	}{{0, 0}, {1, 50}} {
		t.Run(fmt.Sprint(tt.ratio), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Autoscaler.KeepRatio = tt.ratio
			h := newHarness(t, cfg)
			h.proc.draw = goldenDraw()
			lines := make([]string, 50)
			for i := range lines {
				lines[i] = autoscalerRow("6", "6")
			}

			out := flatten(h.runBatch(batchOf(lines)))

			if len(out) != tt.kept {
				t.Errorf("kept %d, want %d", len(out), tt.kept)
			}
		})
	}
}

func TestAutoscaler_KeepRatioOutsideZeroToOne_FailsValidate(t *testing.T) {
	for _, tt := range []struct {
		ratio float64
		ok    bool
	}{{-0.1, false}, {1.1, false}, {math.NaN(), false}, {math.Inf(1), false}, {0, true}, {0.2, true}, {1, true}} {
		cfg := defaultConfig()
		cfg.Autoscaler.KeepRatio = tt.ratio

		err := cfg.Validate()

		if (err == nil) != tt.ok {
			t.Errorf("ratio %v: Validate() = %v, want ok = %v", tt.ratio, err, tt.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "keep_ratio") {
			t.Errorf("ratio %v: the error %q does not name keep_ratio", tt.ratio, err)
		}
	}
}

func TestAutoscaler_DroppedRows_KeepTheOrderOfTheRest(t *testing.T) {
	cfg := defaultConfig()
	cfg.Autoscaler.KeepRatio = 0
	h := newHarness(t, cfg)

	out := flatten(h.runBatch(batchOf([]string{
		autoscalerRow("1", "2"),
		autoscalerRow("7", "7"),
		`{"foo":"bar"}`,
		autoscalerRow("4", "9"),
	})))

	var bodies []string
	for _, r := range out {
		bodies = append(bodies, r.body)
	}
	want := []string{"autoscaler nodes 1/2", `{"foo":"bar"}`, "autoscaler nodes 4/9"}
	if strings.Join(bodies, "|") != strings.Join(want, "|") {
		t.Fatalf("bodies = %q, want %q", bodies, want)
	}
}

func TestAutoscaler_ResourceWithOnlyDroppedRows_LeavesNothingBehind(t *testing.T) {
	cfg := defaultConfig()
	cfg.Autoscaler.KeepRatio = 0
	h := newHarness(t, cfg)
	ld := plog.NewLogs()
	kept := ld.ResourceLogs().AppendEmpty()
	kept.Resource().Attributes().PutStr("host.name", "node-a")
	kept.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr(autoscalerRow("1", "3"))
	gone := ld.ResourceLogs().AppendEmpty()
	gone.Resource().Attributes().PutStr("host.name", "node-b")
	sl := gone.ScopeLogs().AppendEmpty()
	for range 3 {
		sl.LogRecords().AppendEmpty().Body().SetStr(autoscalerRow("5", "5"))
	}

	out := h.runBatch(ld)

	if out.ResourceLogs().Len() != 1 || out.LogRecordCount() != 1 {
		t.Fatalf("%d resources and %d records out, want 1 and 1", out.ResourceLogs().Len(), out.LogRecordCount())
	}
	if got := out.ResourceLogs().At(0).Resource().Attributes().AsRaw()["host.name"]; got != "node-a" {
		t.Errorf("the surviving resource is %v, want node-a", got)
	}
	if got := h.counter(sampledOutMetric, "format")["autoscaler"]; got != 3 {
		t.Errorf("sampled_out = %d, want 3", got)
	}
}
