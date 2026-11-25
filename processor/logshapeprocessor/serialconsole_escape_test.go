// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// The bodies are the console text as the log API gives it: the backslashes are in the string.
const (
	escTS        = `2026-11-23T02:00:01+0000 node-1 `
	escTime      = `2026-11-23T02:00:01.123456789Z`
	escLabel     = "gcp.label.is_text_escaped"
	escGcfsd     = escTS + `gcfsd-v2[2957]: time=\"` + escTime + `\" level=\"info\" msg=\"example message\"`
	escPlain     = escTS + `gcfsd-v2[2957]: time="` + escTime + `" level=info msg="say \"hi\" now"`
	escKlogI     = `I1123 02:00:02.000000 1234 server.go:55] example`
	escBareLevel = escTS + `gcfsd-v2[2957]: time=\"` + escTime + `\" level=info msg=\"example message\"`
)

func escapedInput(body string, label any) input {
	attrs := map[string]any{
		"cloud.resource_id": "serialconsole.googleapis.com/serial_port_1_output",
		"gcp.label.compute.googleapis.com/resource_name": "node-1",
	}
	if label != nil {
		attrs[escLabel] = label
	}
	return input{body: body, attrs: attrs}
}

func escService(res pcommon.Resource) string {
	v, _ := res.Attributes().Get("service.name")
	return v.AsString()
}

func TestSerialConsole_EscapedText_IsUndoneWhenTheRecordCarriesTheLabel(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		label   any
		body    string
		service string
		sev     plog.SeverityNumber
	}{
		{"one layer", escGcfsd, true, "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"one layer, level without quotes", escBareLevel, true, "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"one layer, line break at the end", escGcfsd + `\r\n`, true, "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"three backslashes", escTS + `gcfsd-v2[2957]: time=\\\"` + escTime + `\\\" level=\\\"info\\\" msg=\\\"example message\\\"`, true, "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"containerd", escTS + `containerd-gcfs-grpc[2997]: time=\"` + escTime + `\" level=\"warning\" msg=\"x\"`, true, "x", "containerd-gcfs-grpc", plog.SeverityNumberWarn},
		{"inner escaped quotes", escTS + `gcfsd-v2[2957]: time=\"` + escTime + `\" level=\"info\" msg=\"say \\\"hi\\\" now\"`, true, `say "hi" now`, "gcfsd-v2", plog.SeverityNumberInfo},
		{"label as the text true", escGcfsd, "true", "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"label as the text True", escGcfsd, "True", "example message", "gcfsd-v2", plog.SeverityNumberInfo},
		{"plain quotes with the label", escPlain, true, `say "hi" now`, "gcfsd-v2", plog.SeverityNumberInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lr, res := newHarness(t, defaultConfig()).run(escapedInput(c.line, c.label))

			if lr.Body().Str() != c.body || escService(res) != c.service || lr.SeverityNumber() != c.sev {
				t.Fatalf("body=%q service=%q severity=%d, want body=%q service=%q severity=%d",
					lr.Body().Str(), escService(res), lr.SeverityNumber(), c.body, c.service, c.sev)
			}
			if got := attrStr(t, lr.Attributes(), "log.record.original"); got != c.line {
				t.Fatalf("log.record.original = %q, want the raw line", got)
			}
			if _, ok := lr.Attributes().Get(escLabel); !ok {
				t.Fatal("the label was removed from the record")
			}
		})
	}
}

func TestSerialConsole_EscapedText_IsLeftAsItWasWithoutTheLabel(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		label any
		body  string
		sev   plog.SeverityNumber
	}{
		{"no label", escBareLevel, nil, `"example`, plog.SeverityNumberInfo},
		{"no label, quoted level", escGcfsd, nil, `"example`, plog.SeverityNumberUnspecified},
		{"label false", escBareLevel, false, `"example`, plog.SeverityNumberInfo},
		{"label text false", escBareLevel, "false", `"example`, plog.SeverityNumberInfo},
		{"label text other", escBareLevel, "yes", `"example`, plog.SeverityNumberInfo},
		{"plain quotes, no label", escPlain, nil, `say "hi" now`, plog.SeverityNumberInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lr, res := newHarness(t, defaultConfig()).run(escapedInput(c.line, c.label))

			if lr.Body().Str() != c.body || escService(res) != "gcfsd-v2" || lr.SeverityNumber() != c.sev {
				t.Fatalf("body=%q service=%q severity=%d, want body=%q service=gcfsd-v2 severity=%d",
					lr.Body().Str(), escService(res), lr.SeverityNumber(), c.body, c.sev)
			}
		})
	}
}

func TestSerialConsole_KlogLine_TakesItsSeverityFromTheLevelLetter(t *testing.T) {
	cases := []struct {
		name string
		text string
		sev  plog.SeverityNumber
	}{
		{"info", escKlogI, plog.SeverityNumberInfo},
		{"warning", `W1123 02:00:02.000000 1234 server.go:55] example`, plog.SeverityNumberWarn},
		{"error", `E1123 02:00:02.000000 1234 server.go:55] example`, plog.SeverityNumberError},
		{"fatal", `F1123 02:00:02.000000 1234 server.go:55] example`, plog.SeverityNumberFatal},
		{"level key wins", `E1123 02:00:02.000000 1234 server.go:55] level=info example`, plog.SeverityNumberInfo},
		{"not a klog header", `X1123 02:00:02.000000 1234 server.go:55] example`, plog.SeverityNumberUnspecified},
		{"no space after the bracket", `I1123 02:00:02.000000 1234 server.go:55]example`, plog.SeverityNumberUnspecified},
	}
	for _, label := range []any{nil, true} {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				lr, res := newHarness(t, defaultConfig()).run(escapedInput(escTS+`kubelet[1234]: `+c.text, label))

				if lr.Body().Str() != c.text || escService(res) != "kubelet" || lr.SeverityNumber() != c.sev {
					t.Fatalf("body=%q service=%q severity=%d, want body=%q service=kubelet severity=%d",
						lr.Body().Str(), escService(res), lr.SeverityNumber(), c.text, c.sev)
				}
			})
		}
	}
}

func TestSerialConsole_MapBodyWithoutText_IsNotClaimed(t *testing.T) {
	in := escapedInput("", true)
	in.bodyMap = map[string]any{"textPayload": "x"}

	lr, res := newHarness(t, defaultConfig()).run(in)

	if lr.Body().Type() != pcommon.ValueTypeMap || !maps.Equal(lr.Body().Map().AsRaw(), map[string]any{"textPayload": "x"}) {
		t.Fatalf("body = %v (%s), want the map unchanged", lr.Body().AsRaw(), lr.Body().Type())
	}
	absent(t, lr.Attributes(), "log.record.original", "process.pid")
	absent(t, res.Attributes(), "host.name", "service.name")
}

func TestSerialConsole_MapBodyWithText_KeepsTheMapAndShapesTheText(t *testing.T) {
	in := escapedInput("", true)
	in.bodyMap = map[string]any{"text": escGcfsd, "other": "kept"}

	lr, res := newHarness(t, defaultConfig()).run(in)

	want := map[string]any{"text": "example message", "other": "kept"}
	if lr.Body().Type() != pcommon.ValueTypeMap || !maps.Equal(lr.Body().Map().AsRaw(), want) {
		t.Fatalf("body = %v (%s), want %v", lr.Body().AsRaw(), lr.Body().Type(), want)
	}
	if escService(res) != "gcfsd-v2" || lr.SeverityNumber() != plog.SeverityNumberInfo {
		t.Fatalf("service=%q severity=%d", escService(res), lr.SeverityNumber())
	}
}

func TestUnescapeQuotes_PeelsLayersWhileTheFirstQuoteIsEscaped(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no quote", `abc`, `abc`},
		{"plain first quote", `a="b \"c\""`, `a="b \"c\""`},
		{"one layer", `a=\"b\"`, `a="b"`},
		{"two backslashes", `a=\\"b\\"`, `a="b"`},
		{"four backslashes", `a=\\\\"b\\\\"`, `a="b"`},
		{"cap of three layers", `x=\\\\\\\\"y`, `x=\"y`},
		{"inner quotes survive", `a=\"b \\"c\\" d\"`, `a="b \"c\" d"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unescapeQuotes(c.in); got != c.want {
				t.Fatalf("unescapeQuotes(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
