// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/rename"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

var (
	zapWrapper   = regexp.MustCompile(`^[A-Za-z0-9_.-]+\[(\d+)\]: `)
	zapTimeShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})$`)
	zapLevel     = regexp.MustCompile(`^[a-z]+$`)
	zapCaller    = regexp.MustCompile(`^\S+:\d+$`)
	callerModule = regexp.MustCompile(`@[^/]+`)
)

// consoleLine is a collector console line: ts TAB level TAB caller TAB message [TAB json].
type consoleLine struct {
	pid                            int64
	hasPID                         bool
	ts, level, caller, msg, fields string
}

// splitZap reads a console line, with the syslog wrapper and its #011 tabs when present.
func splitZap(s string) (consoleLine, bool) {
	var z consoleLine
	if m := zapWrapper.FindStringSubmatch(s); m != nil {
		z.pid, _ = strconv.ParseInt(m[1], 10, 64)
		z.hasPID = true
		s = s[len(m[0]):]
	}
	if !strings.Contains(s, "\t") {
		s = strings.ReplaceAll(s, "#011", "\t")
	}
	parts := strings.SplitN(s, "\t", 5)
	if len(parts) < 4 || !zapTimeShape.MatchString(parts[0]) || !zapLevel.MatchString(parts[1]) || !zapCaller.MatchString(parts[2]) {
		return consoleLine{}, false
	}
	z.ts, z.level, z.caller, z.msg = parts[0], parts[1], parts[2], parts[3]
	if len(parts) == 5 {
		z.fields = parts[4]
	}
	return z, true
}

// collectorBranch shapes the own log lines of every collector. A node-logs row keeps the full
// line in original_message and a JSON tail as its body; the branch reads the line from there.
type collectorBranch struct{}

func newCollectorBranch() *collectorBranch { return &collectorBranch{} }

func (*collectorBranch) name() string { return "collector" }

// claims takes a console line only when its fields name a collector component, so other zap
// console lines stay with the logrus branch.
func (*collectorBranch) claims(r *rec) bool {
	if z, ok := splitZap(r.body); ok {
		return strings.Contains(z.fields, `"otelcol.component`)
	}
	z, ok := splitZap(originalMessage(r))
	return ok && strings.Contains(z.fields, `"otelcol.component`)
}

func originalMessage(r *rec) string {
	if v, ok := r.lr.Attributes().Get("original_message"); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

// putCollectorFields writes the fields of the JSON tail: otelcol.* names belong to the collector
// and stay, error is the exception message, and every other key goes under vstan.collector.
func putCollectorFields(a pcommon.Map, obj *jsonObject) {
	for _, k := range obj.rest() {
		v := obj.vals[k]
		switch {
		case strings.HasPrefix(k, "otelcol."):
			putValue(a, k, v)
		case k == "error":
			if s, ok := v.(string); ok {
				a.PutStr("exception.message", s)
			} else {
				putValue(a, "vstan.collector.error", v)
			}
		default:
			putValue(a, "vstan.collector."+k, v)
		}
	}
}

func (*collectorBranch) parse(r *rec) error {
	src := r.body
	z, ok := splitZap(src)
	if !ok {
		src = originalMessage(r)
		if z, ok = splitZap(src); !ok {
			return errors.New("not a collector line")
		}
	}
	ts, err := parseTime(z.ts)
	if err != nil {
		return err
	}
	obj := &jsonObject{vals: map[string]any{}}
	if strings.TrimSpace(z.fields) != "" {
		if obj, err = parseJSONObject(z.fields); err != nil {
			return err
		}
	}

	obj.take("resource")
	obj.take("otelcol.signal")
	kind := obj.takeStr("otelcol.component.kind")
	id := obj.takeStr("otelcol.component.id")
	level, _ := severity.FromSource(severity.Word, z.level)
	text := z.msg
	debugExporter := kind == "exporter" && (id == "debug" || strings.HasPrefix(id, "debug/"))
	if debugExporter {
		level, _ = severity.FromSource(severity.Word, "debug")
		var pairs []body.Pair
		for _, k := range obj.rest() {
			pairs = append(pairs, body.Pair{Key: k, Value: scalarText(obj.vals[k])})
		}
		text = body.Compose("", z.msg, pairs...)
		obj = &jsonObject{vals: map[string]any{}} // the counts stay in the Body only
	}

	a := r.lr.Attributes()
	a.PutStr("log.record.original", src)
	if z.hasPID {
		a.PutInt("process.pid", z.pid)
	}
	if path, line, ok := splitCaller(z.caller); ok {
		a.PutStr("code.file.path", callerModule.ReplaceAllString(path, ""))
		a.PutInt("code.line.number", line)
	}
	if id != "" {
		a.PutStr("otel.component.name", id)
	}
	putCollectorFields(a, obj)
	rename.Apply(a)

	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	setSeverity(r.lr, level)
	r.lr.Body().SetStr(text)
	if id != "" {
		r.scope.SetName(id)
	}
	return nil
}
