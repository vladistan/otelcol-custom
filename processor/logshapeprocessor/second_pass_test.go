// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// A record that went through the edge collector goes through the gateway collector again. Each sample
// line, shaped once and shaped a second time, must come out the same.
func TestSecondPass_EverySampleLineComesOutAsItDidTheFirstTime(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			first := newHarness(t, defaultConfig()).runBatch(batchOf(lines))
			once := flatten(first)

			twice := flatten(newHarness(t, defaultConfig()).runBatch(first))

			if len(once) != len(twice) {
				t.Fatalf("%d records after one pass, %d after two", len(once), len(twice))
			}
			for i := range once {
				if !reflect.DeepEqual(once[i], twice[i]) {
					t.Errorf("line %d changed on the second pass:\nfirst:  %+v\nsecond: %+v", i, once[i], twice[i])
				}
			}
		})
	}
}

func TestSecondPass_Smartd_KeepsTheRawLine(t *testing.T) {
	const raw = "Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors"

	lr, _ := secondPass(t, input{body: raw})

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != raw {
		t.Fatalf("log.record.original = %q, want %q", got, raw)
	}
}

func TestSecondPass_StallCheck_KeepsTheRawLineWithItsPrefix(t *testing.T) {
	const raw = "2026-11-20T00:03:18.148343Z stdout F published StallCount=3"

	lr, _ := secondPass(t, input{body: raw})

	w4Body(t, lr, "published StallCount=3")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != raw {
		t.Fatalf("log.record.original = %q, want %q", got, raw)
	}
}

func TestSecondPass_StallCheck_FirstPassWritesTheRawLine(t *testing.T) {
	const raw = "2026-11-20T00:03:18.148343Z stdout F published StallCount=3"

	lr, _ := runW4(t, input{body: raw})

	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != raw {
		t.Fatalf("log.record.original = %q, want %q", got, raw)
	}
}

// secondPass shapes the record twice, with one processor each time, and returns the second result.
func secondPass(t *testing.T, in input) (plog.LogRecord, pcommon.Resource) {
	t.Helper()
	first := newHarness(t, defaultConfig())
	first.run(in)
	out := first.sink.AllLogs()[0]
	second := newHarness(t, defaultConfig())
	second.runBatch(out)
	rl := second.sink.AllLogs()[0].ResourceLogs().At(0)
	return rl.ScopeLogs().At(0).LogRecords().At(0), rl.Resource()
}
