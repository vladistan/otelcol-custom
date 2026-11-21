// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"slices"
	"testing"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/klog"
)

// shapes lists the records of a batch as text, in a fixed order, so two batches compare by their content.
func shapes(records []outRecord) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = fmt.Sprint(r.body, "|", r.severity, "|", r.severityText, "|", r.scope, "|", r.resource, "|", r.attrs)
	}
	slices.Sort(out)
	return out
}

// Claims leaves what it parsed on the record for parse. A line must give the same record whether it
// follows other lines in a batch or comes alone.
func TestMemo_EveryLineGivesTheSameRecordInABatchAndAlone(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			h := newHarness(t, defaultConfig())
			inBatch := shapes(flatten(h.runBatch(batchOf(lines))))
			var alone []outRecord
			for _, line := range lines {
				alone = append(alone, flatten(newHarness(t, defaultConfig()).runBatch(batchOf([]string{line})))...)
			}

			if got := shapes(alone); !slices.Equal(inBatch, got) {
				t.Errorf("a batch gives\n%v\nthe lines one by one give\n%v", inBatch, got)
			}
		})
	}
}

// The batch test above can only see a stale result when the samples of a format differ; this sends the
// samples of one format twice, interleaved with the samples of every other.
func TestMemo_AMixedBatchGivesTheSameRecordsAsEachFormatAlone(t *testing.T) {
	samples := loadSamples(t)
	var mixed, want []string
	for _, lines := range samples {
		mixed = append(mixed, lines...)
	}
	for _, lines := range samples {
		want = append(want, shapes(flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))))...)
	}

	got := shapes(flatten(newHarness(t, defaultConfig()).runBatch(batchOf(mixed))))

	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("a mixed batch differs from the formats one by one:\n%v\nwant\n%v", got, want)
	}
}

func TestMemo_TheKlogQuickCheckAndTheParseDecideLikeIsKlog(t *testing.T) {
	inputs := append(oddInputs(t), "I1119 23:05:39.184859       1 a.go:1] ok", "F0102 15:04:05.000000 1 x.go:2] x", "W12 x")
	for _, text := range inputs {
		want := klog.IsKlog(text)
		if got := klog.QuickCheck(text) && klog.ParseKlog(text) != nil; got != want {
			t.Fatalf("%q: quick check and parse say %v, IsKlog says %v", shorten(text), got, want)
		}
	}
}
