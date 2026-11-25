// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestKernelAudit_TrailingNewlineOrLeadingSpace_GivesTheBodyOfTheCleanLine(t *testing.T) {
	for name, body := range map[string]string{
		"trailing LF":   auditAllowed + "\n",
		"trailing CRLF": auditAllowed + "\r\n",
		"leading space": " " + auditAllowed,
		"both":          "  " + auditAllowed + " \r\n",
	} {
		t.Run(name, func(t *testing.T) {
			lr, res := runW4(t, input{body: body})

			w4Body(t, lr, auditBodyText)
			if attrStr(t, res.Attributes(), "service.name") != "audit" {
				t.Fatalf("the record was not claimed: %v", res.Attributes().AsRaw())
			}
		})
	}
}

func TestKernelAudit_OnlyWhitespace_IsNotClaimed(t *testing.T) {
	const body = " \r\n "

	lr, _ := runW4(t, input{body: body})

	w4Untouched(t, lr, body, map[string]any{})
}

func gcplbBody(t *testing.T, over map[string]any) string {
	t.Helper()
	lr, _ := gcplbRun(t, gcplbOnly(nil), input{attrs: gcplbAttrs(over), resource: gcplbResource()})
	return lr.Body().Str()
}

func TestGCPLB_DurationFromEachOfTheThreeNames_GivesTheSameBody(t *testing.T) {
	want := gcplbBody(t, nil)
	if !strings.HasSuffix(want, " 40s") {
		t.Fatalf("the baseline body has no duration: %q", want)
	}

	for name, over := range map[string]map[string]any{
		"http.server.duration": {"http.request.server.duration": nil, "http.server.duration": 40.0},
		"server.duration":      {"http.request.server.duration": nil, "server.duration": 40.0},
	} {
		if got := gcplbBody(t, over); got != want {
			t.Errorf("%s: body = %q, want %q", name, got, want)
		}
	}
}

func TestGCPLB_TwoDurationNames_TheFirstInOrderWins(t *testing.T) {
	want := gcplbBody(t, nil)

	got := gcplbBody(t, map[string]any{"http.server.duration": 5.0, "server.duration": 9.0})
	second := gcplbBody(t, map[string]any{"http.request.server.duration": nil, "http.server.duration": 40.0, "server.duration": 9.0})

	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if second != want {
		t.Errorf("body with the last two names = %q, want %q", second, want)
	}
}

func TestGCPLB_NoDurationName_BodyHasNoDuration(t *testing.T) {
	with := gcplbBody(t, nil)

	got := gcplbBody(t, map[string]any{"http.request.server.duration": nil})

	if !strings.HasPrefix(with, got+" ") {
		t.Fatalf("body = %q, want %q without its duration", got, with)
	}
}

func TestGCPLB_FirstNameSetIsNotANumber_BodyHasNoDuration(t *testing.T) {
	with := gcplbBody(t, nil)

	got := gcplbBody(t, map[string]any{"http.request.server.duration": "slow", "http.server.duration": 5.0})

	if !strings.HasPrefix(with, got+" ") {
		t.Fatalf("body = %q, want %q without its duration", got, with)
	}
}

func TestLogrus_ZapServiceKey_GivesServiceNameAndNoLogrusField(t *testing.T) {
	lr, res := runW4(t, input{body: `{"level":"info","ts":1795129202.123,"caller":"main.go:10","msg":"started","service":"host-sync-bot","version":"1.2"}`})

	if got := attrStr(t, res.Attributes(), "service.name"); got != "host-sync-bot" {
		t.Fatalf("service.name = %q", got)
	}
	absent(t, lr.Attributes(), "vstan.logrus.service")
	if attrStr(t, lr.Attributes(), "vstan.logrus.version") != "1.2" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestLogrus_NoServiceKey_LeavesServiceNameAlone(t *testing.T) {
	_, res := runW4(t, input{
		body:     `{"level":"info","ts":1795129202.123,"msg":"started"}`,
		resource: map[string]any{"service.name": "from-the-edge"},
	})

	if got := attrStr(t, res.Attributes(), "service.name"); got != "from-the-edge" {
		t.Fatalf("service.name = %q", got)
	}
}

func TestLogrus_ServiceKeyThatIsNotAString_IsIgnored(t *testing.T) {
	_, res := runW4(t, input{body: `{"level":"info","ts":1795129202.123,"msg":"started","service":7}`})

	absent(t, res.Attributes(), "service.name")
}

func TestK8s_ObjectEvent_NoteWinsOverMessageAndMessageFillsIn(t *testing.T) {
	const head = `{"involvedObject":{"kind":"Pod","name":"p-1","namespace":"team-a"},"reason":"Pulled",`
	for name, c := range map[string]struct{ tail, want string }{
		"note only":        {`"note":"image pulled","type":"Normal"}`, "Pulled: image pulled"},
		"message only":     {`"message":"image pulled","type":"Normal"}`, "Pulled: image pulled"},
		"both":             {`"note":"from the note","message":"from the message","type":"Normal"}`, "Pulled: from the note"},
		"neither":          {`"type":"Normal"}`, "Pulled"},
		"empty note":       {`"note":"","message":"from the message","type":"Normal"}`, "Pulled: from the message"},
		"warning keeps it": {`"note":"back-off","type":"Warning"}`, "Pulled: back-off"},
	} {
		t.Run(name, func(t *testing.T) {
			lr, _ := runW4(t, input{body: head + c.tail})

			w4Body(t, lr, c.want)
			if strings.Contains(c.tail, "Warning") {
				w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
			}
		})
	}
}
