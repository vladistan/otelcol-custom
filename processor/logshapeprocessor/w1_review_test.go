// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import "testing"

func TestJSONBranches_ReceiverOriginalMessage_IsReplacedByTheOriginalLine(t *testing.T) {
	for name, line := range map[string]string{
		"tracing": tracingInfo, "gcpjson": gcpInfo, "pino": pinoError, "mongod": mongodEnded,
	} {
		lr, _ := onlyHarness(t, name).runScoped(input{body: line, attrs: map[string]any{"original_message": "an earlier copy"}})

		absent(t, lr.Attributes(), "original_message")
		if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
			t.Errorf("%s: log.record.original = %q, want the shaped line", name, got)
		}
	}
}
