// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"testing"
)

// retiredSources are the log.source values a scrape collector on an older release still writes; no
// renamed branch claims them.
var retiredSources = []string{"aws.elb", "aws.eks", "aws.s3access", "gcp.lb", "gcp.audit", "aws.cloudtrail"}

func TestSourceRename_RecordWithARetiredValue_PassesUnchanged(t *testing.T) {
	for _, old := range retiredSources {
		t.Run(old, func(t *testing.T) {
			lr, _ := newHarness(t, defaultConfig()).run(input{attrs: map[string]any{"log.source": old}})

			w4Untouched(t, lr, "", map[string]any{"log.source": old})
		})
	}
}

func TestSourceRename_RecordWithARetiredValue_IsCountedUnderThatValue(t *testing.T) {
	for _, old := range retiredSources {
		t.Run(old, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			h.run(input{attrs: map[string]any{"log.source": old}})

			if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{old: 1}) {
				t.Fatalf("unmatched = %v, want %s once", got, old)
			}
		})
	}
}
