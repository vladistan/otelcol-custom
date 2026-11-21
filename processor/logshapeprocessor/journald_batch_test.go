// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// A journal entry of five lines is one record: the body is the first line and full_message holds
// all five. The processor must send one record out, shape the first line when a branch claims it,
// and keep every line in full_message. The lines are invented in the shape of a real journal row.
var journalFiveLines = []string{
	"07:13:58 - LiteLLM Proxy:INFO: policy_registry.py:577 - Synced 0 production policies",
	"07:13:58 - LiteLLM Proxy:INFO: attachment_registry.py:479 - Synced 0 attachments",
	"07:13:58 - LiteLLM Proxy:INFO: proxy_server.py:5188 - Initialized the proxy",
	"07:13:59 - LiteLLM Proxy:INFO: tool_registry_writer.py:329 - Wrote 3 tools",
	"07:13:59 - LiteLLM Proxy:INFO: proxy_server.py:5200 - Ready",
}

func journalBatch(first string, rest ...string) input {
	return input{
		body:     first,
		attrs:    map[string]any{"log.source": "journal", "full_message": strings.Join(append([]string{first}, rest...), "\n")},
		resource: map[string]any{"service.name": "litellm"},
	}
}

func TestJournaldBatch_FiveLinesClaimedByABranch_StayOneRecordWithAllFiveLines(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := journalBatch(journalFiveLines[0], journalFiveLines[1:]...)

	lr, _ := h.run(in)

	if n := h.sink.LogRecordCount(); n != 1 {
		t.Fatalf("records out = %d, want 1", n)
	}
	w4Body(t, lr, "Synced 0 production policies")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	if got := attrStr(t, lr.Attributes(), "full_message"); got != strings.Join(journalFiveLines, "\n") || strings.Count(got, "\n") != 4 {
		t.Fatalf("full_message = %q, want all five lines", got)
	}
}

func TestJournaldBatch_FiveLinesNoBranchClaims_StayOneRecordUnchanged(t *testing.T) {
	h := newHarness(t, defaultConfig())
	lines := []string{"alpha one", "beta two", "gamma three", "delta four", "epsilon five"}
	in := journalBatch(lines[0], lines[1:]...)

	lr, _ := h.run(in)

	if n := h.sink.LogRecordCount(); n != 1 {
		t.Fatalf("records out = %d, want 1", n)
	}
	w4Body(t, lr, "alpha one")
	if got := attrStr(t, lr.Attributes(), "full_message"); got != strings.Join(lines, "\n") {
		t.Fatalf("full_message = %q, want all five lines", got)
	}
}

func TestJournaldBatch_FiveLineBodyStaysOneRecord(t *testing.T) {
	h := newHarness(t, defaultConfig())
	body := strings.Join([]string{"first plain line", "second", "third", "fourth", "fifth"}, "\n")

	lr, _ := h.run(input{body: body, attrs: map[string]any{"log.source": "journal"}})

	if n := h.sink.LogRecordCount(); n != 1 {
		t.Fatalf("records out = %d, want 1", n)
	}
	if lr.Body().Str() != body {
		t.Fatalf("body = %q, want the five lines kept", lr.Body().Str())
	}
}
