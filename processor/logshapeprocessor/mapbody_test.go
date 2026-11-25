// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// journaldMap is a journald record as the receiver makes it: the text under key and the journal fields beside it.
func journaldMap(key, text string) map[string]any {
	return map[string]any{
		key:                 text,
		"_HOSTNAME":         "host-a",
		"_PID":              "4242",
		"SYSLOG_IDENTIFIER": "kernel",
		"PRIORITY":          "6",
	}
}

func bodyMapOf(t *testing.T, lr plog.LogRecord) map[string]any {
	t.Helper()
	if lr.Body().Type() != pcommon.ValueTypeMap {
		t.Fatalf("body is %s, want a map: %q", lr.Body().Type(), lr.Body().AsString())
	}
	return lr.Body().Map().AsRaw()
}

const auditBodyText = "[apparmor] ALLOWED file_perm send, profile snap.amazon-ssm-agent.amazon-ssm-agent, to 203.0.113.10:443"

func TestMapBody_TextKey_ShapedTextGoesBackUnderTextWithEveryOtherKey(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: journaldMap("text", auditAllowed)})

	got := bodyMapOf(t, lr)

	want := journaldMap("text", auditBodyText)
	delete(want, "PRIORITY") // the branch set the severity
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %v, want %v", got, want)
	}
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestMapBody_MessageKey_ShapedTextGoesBackUnderMessageOnly(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: journaldMap("MESSAGE", auditAllowed)})

	got := bodyMapOf(t, lr)

	if got["MESSAGE"] != auditBodyText {
		t.Fatalf("MESSAGE = %v, want the shaped text", got["MESSAGE"])
	}
	if _, has := got["text"]; has {
		t.Fatalf("a text key was added: %v", got)
	}
	if got["_HOSTNAME"] != "host-a" || got["SYSLOG_IDENTIFIER"] != "kernel" || got["_PID"] != "4242" {
		t.Fatalf("a journal field was lost: %v", got)
	}
}

func TestMapBody_TextAndMessage_ReadsTextAndLeavesMessageAlone(t *testing.T) {
	m := journaldMap("text", auditAllowed)
	m["MESSAGE"] = "the journal line"

	lr, _ := runW4(t, input{bodyMap: m})

	got := bodyMapOf(t, lr)
	if got["text"] != auditBodyText || got["MESSAGE"] != "the journal line" {
		t.Fatalf("body = %v", got)
	}
}

func TestMapBody_SeverityChanged_DropsPriority(t *testing.T) {
	lr, _ := runW4(t, input{bodyMap: journaldMap("MESSAGE", auditAllowed)})

	if _, has := bodyMapOf(t, lr)["PRIORITY"]; has {
		t.Fatal("PRIORITY stayed after the branch set the severity")
	}
}

func TestMapBody_SeverityKept_KeepsPriority(t *testing.T) {
	lr, _ := runW4(t, input{
		bodyMap:  journaldMap("MESSAGE", auditAllowed),
		severity: plog.SeverityNumberInfo, severityText: "INFO",
	})

	got := bodyMapOf(t, lr)
	if got["PRIORITY"] != "6" {
		t.Fatalf("PRIORITY = %v, want it kept: %v", got["PRIORITY"], got)
	}
	if got["MESSAGE"] != auditBodyText {
		t.Fatalf("MESSAGE = %v, want the shaped text", got["MESSAGE"])
	}
}

func TestMapBody_BranchThatLeavesTheBody_StillDropsPriorityWhenItSetsTheSeverity(t *testing.T) {
	const line = "Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors"

	lr, _ := runW4(t, input{bodyMap: journaldMap("MESSAGE", line)})

	got := bodyMapOf(t, lr)
	if got["MESSAGE"] != line {
		t.Fatalf("MESSAGE = %v, want the line as it was", got["MESSAGE"])
	}
	if _, has := got["PRIORITY"]; has {
		t.Fatalf("PRIORITY stayed after smartd set WARN: %v", got)
	}
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestMapBody_NoTextAndNoMessage_PassesUnchangedAndIsCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	m := map[string]any{"_HOSTNAME": "host-a", "PRIORITY": "6", "other": "x"}

	lr, _ := h.run(input{bodyMap: m})

	if got := bodyMapOf(t, lr); !reflect.DeepEqual(got, m) {
		t.Fatalf("body = %v, want it unchanged", got)
	}
	if n := sumOf(h.counter(unmatchedMetric, "source")); n != 1 {
		t.Fatalf("unmatched = %d, want 1", n)
	}
}

func TestMapBody_NonStringMessage_PassesUnchangedAndIsCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	m := map[string]any{"MESSAGE": []byte(auditAllowed), "_HOSTNAME": "host-a", "PRIORITY": "6"}

	lr, _ := h.run(input{bodyMap: m})

	if got := bodyMapOf(t, lr); !reflect.DeepEqual(got, m) {
		t.Fatalf("body = %v, want it unchanged", got)
	}
	if n := sumOf(h.counter(unmatchedMetric, "source")); n != 1 {
		t.Fatalf("unmatched = %d, want 1", n)
	}
}

func TestMapBody_UnclaimedText_PassesUnchanged(t *testing.T) {
	m := journaldMap("MESSAGE", "alpha one")

	lr, _ := runW4(t, input{bodyMap: m})

	if got := bodyMapOf(t, lr); !reflect.DeepEqual(got, m) {
		t.Fatalf("body = %v, want it unchanged", got)
	}
}

func TestMapBody_StringBody_StaysAString(t *testing.T) {
	lr, _ := runW4(t, input{body: auditAllowed})

	w4Body(t, lr, auditBodyText)
}

func sumOf(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}
