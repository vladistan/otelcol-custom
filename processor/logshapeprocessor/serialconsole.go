// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/severity"
	"github.com/vladistan/otelcol-custom/shape/strip"
)

const serialHostLabel = "gcp.label.compute.googleapis.com/resource_name"

var (
	okMark     = regexp.MustCompile(`^\[\s*OK\s*\]`)
	failedMark = regexp.MustCompile(`^\[\s*FAILED\s*\]`)
	jobMark    = regexp.MustCompile(`^\[\s*\*+\s*\].*running`)
)

// serialConsoleBranch shapes GCE serial console records: the escaped text is unescaped and stripped
// of ANSI, the host comes from the instance label, and the systemd status marker gives the severity.
// A record that is only a line break keeps an empty body for a later drop. A node line with a
// tag[pid] part also gets service.name, process.pid, the level= severity and the msg= body. Escaped
// quotes of such a line are undone when the record carries the is-text-escaped label, and a klog
// header gives the severity when no level= is there. A map body with no text key is not claimed.
type serialConsoleBranch struct{}

func newSerialConsoleBranch() *serialConsoleBranch { return &serialConsoleBranch{} }

func (*serialConsoleBranch) name() string { return "serialconsole" }

func (*serialConsoleBranch) claims(r *rec) bool {
	if r.body == "" && r.lr.Body().Type() != pcommon.ValueTypeStr {
		return false
	}
	return strings.Contains(attrText(r.lr.Attributes(), "cloud.resource_id"), "serialconsole.googleapis.com") ||
		strings.Contains(attrText(r.res.Attributes(), "cloud.resource_id"), "serialconsole.googleapis.com")
}

// A GKE node line: a UTC timestamp, the node, then tag[pid]: and the text.
var (
	taggedLine = regexp.MustCompile(`(?s)^\d{4}-\d\d-\d\dT[\d:.]+\+0000 \S+ ([^\s\[\]:]+)\[(\d+)\]: (.*)$`)
	levelKey   = regexp.MustCompile(`(?:^|\s)level="?(\w+)`)
	msgKey     = regexp.MustCompile(`(?:^|\s)msg=(?:"((?:[^"\\]|\\.)*)"|(\S+))`)
	shaEntry   = `[\w./-]+[=:]sha256:[0-9a-f]{64}`
	shaEntries = regexp.MustCompile(shaEntry + `(?:[,\s]+` + shaEntry + `)+`)
	shaOne     = regexp.MustCompile(shaEntry)
)

// serialTagged is a node line split at its tag.
type serialTagged struct {
	service string
	pid     int64
	text    string
}

func splitTagged(text string) (serialTagged, bool) {
	m := taggedLine.FindStringSubmatch(text)
	if m == nil {
		return serialTagged{}, false
	}
	pid, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return serialTagged{}, false
	}
	return serialTagged{service: m[1], pid: pid, text: m[3]}, true
}

// taggedBody is the msg= value without its quotes, else the whole text, with sha256 label runs counted.
func taggedBody(text string) string {
	body := text
	if m := msgKey.FindStringSubmatch(text); m != nil {
		body = strings.ReplaceAll(m[1]+m[2], `\"`, `"`)
	}
	return shaEntries.ReplaceAllStringFunc(body, func(run string) string {
		return fmt.Sprintf("[%d sha256 labels]", len(shaOne.FindAllString(run, -1)))
	})
}

// textEscapedLabel is the record label the log API sets when the console text is escaped.
const textEscapedLabel = "gcp.label.is_text_escaped"

var (
	klogHeader = regexp.MustCompile(`^([IWEF])\d{4} \d\d:\d\d:\d\d\.\d+\s+\d+ \S+:\d+\] `)
	klogWord   = map[string]string{"I": "info", "W": "warn", "E": "error", "F": "fatal"}
	quoteLayer = strings.NewReplacer(`\\`, `\`, `\"`, `"`)
)

// textEscaped reports whether the record says its text is escaped, as a bool or as the text true.
func textEscaped(a pcommon.Map) bool {
	v, ok := a.Get(textEscapedLabel)
	if !ok {
		return false
	}
	switch v.Type() {
	case pcommon.ValueTypeBool:
		return v.Bool()
	case pcommon.ValueTypeStr:
		return strings.EqualFold(v.Str(), "true")
	}
	return false
}

// unescapeQuotes peels up to three layers of escaped quotes. It stops once the first quote of the text
// is not escaped, so the escaped quotes inside a quoted value stay.
func unescapeQuotes(text string) string {
	for range 3 {
		i := strings.IndexByte(text, '"')
		if i <= 0 || text[i-1] != '\\' {
			return text
		}
		text = quoteLayer.Replace(text)
	}
	return text
}

func markerLevel(text string) severity.Level {
	switch {
	case okMark.MatchString(text):
		return wordLevel("info")
	case failedMark.MatchString(text):
		return wordLevel("error")
	case jobMark.MatchString(text):
		return wordLevel("warn")
	}
	return severity.Level{}
}

func (*serialConsoleBranch) parse(r *rec) error {
	text := strip.SerialConsole(r.body)
	level := markerLevel(text)
	body := text
	a := r.lr.Attributes()
	if t, ok := splitTagged(text); ok {
		r.res.Attributes().PutStr(serviceKey, t.service)
		a.PutInt("process.pid", t.pid)
		if textEscaped(a) {
			t.text = unescapeQuotes(t.text)
		}
		body = taggedBody(t.text)
		if m := levelKey.FindStringSubmatch(t.text); m != nil {
			level = wordLevel(m[1])
		} else if m := klogHeader.FindStringSubmatch(t.text); m != nil {
			level = wordLevel(klogWord[m[1]])
		}
	}
	a.PutStr("log.record.original", r.body)
	if host := attrText(a, serialHostLabel); host != "" {
		r.res.Attributes().PutStr("host.name", host)
	}
	applyLevel(r.lr, level)
	r.lr.Body().SetStr(body)
	return nil
}
