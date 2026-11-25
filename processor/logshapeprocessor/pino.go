// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// pinoTagged splits an optional "[TAG] " prefix from the JSON object.
var pinoTagged = regexp.MustCompile(`^\[([A-Za-z0-9_.:-]+)\]\s+(\{.*\})$`)

// pinoLevelNumber is a cheap test that "level" holds a number, so a line with a text level is never parsed here.
var pinoLevelNumber = regexp.MustCompile(`"level"\s*:\s*\d`)

// pinoBranch shapes pino JSON lines: numeric level, epoch time, pid, trace_id and an error object.
type pinoBranch struct{}

func newPinoBranch() *pinoBranch { return &pinoBranch{} }

func (*pinoBranch) name() string { return "pino" }

// splitPino returns the tag and the JSON text of a pino line.
func splitPino(s string) (tag, text string) {
	s = strings.TrimSpace(s)
	if m := pinoTagged.FindStringSubmatch(s); m != nil {
		return m[1], m[2]
	}
	return "", s
}

func (*pinoBranch) claims(r *rec) bool {
	_, s := splitPino(r.body)
	if !strings.HasPrefix(s, "{") || !pinoLevelNumber.MatchString(s) || !strings.Contains(s, `"time"`) {
		return false
	}
	o, err := parseJSONObject(s)
	if err != nil {
		return false
	}
	_, levelIsNumber := o.vals["level"].(int64)
	return levelIsNumber && isNumeric(o.vals["time"])
}

func (*pinoBranch) parse(r *rec) error {
	tag, s := splitPino(r.body)
	o, err := parseJSONObject(s)
	if err != nil {
		return err
	}
	rawLevel, _ := o.take("level")
	level, _ := severity.FromSource(severity.Pino, scalarText(rawLevel))
	rawTime, _ := o.take("time")
	ts, ok := epochTime(rawTime)
	if !ok {
		return errors.New("pino time is not an epoch between 2000 and 2099")
	}
	var traceID [16]byte
	if err := decodeHexID(o.takeStr("trace_id"), traceID[:]); err != nil {
		return err
	}
	var spanID [8]byte
	if err := decodeHexID(o.takeStr("span_id"), spanID[:]); err != nil {
		return err
	}
	pid, hasPID := o.take("pid")
	o.take("hostname")
	msg := o.takeStr("msg")

	a := r.lr.Attributes()
	putOriginal(a, r.body)
	if hasPID {
		n, isInt := pid.(int64)
		if !isInt {
			return errors.New("pino pid is not an integer")
		}
		a.PutInt("process.pid", n)
	}
	// An error object gives the exception attributes and keeps its other fields as
	// vstan.pino.error.<key>; an error that is a plain string is the exception message.
	if text, isText := o.vals["error"].(string); isText {
		o.take("error")
		a.PutStr("exception.message", text)
	}
	if e, isObj := o.object("error"); isObj {
		o.take("error")
		name, _ := e["name"].(string)
		message, _ := e["message"].(string)
		stack, _ := e["stack"].(string)
		putNonEmpty(a, "exception.type", name)
		putNonEmpty(a, "exception.message", message)
		putNonEmpty(a, "exception.stacktrace", stack)
		if msg == "" {
			msg = message
		}
		keys := make([]string, 0, len(e))
		for k := range e {
			if k != "name" && k != "message" && k != "stack" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			putValue(a, "vstan.pino.error."+k, e[k])
		}
	}
	if msg == "" {
		return errors.New("pino line has no message")
	}
	o.putAttrs(a, "vstan.pino.")
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	setSeverity(r.lr, level)
	r.lr.Body().SetStr(msg)
	if traceID != ([16]byte{}) {
		r.lr.SetTraceID(pcommon.TraceID(traceID))
	}
	if spanID != ([8]byte{}) {
		r.lr.SetSpanID(pcommon.SpanID(spanID))
	}
	if tag != "" {
		r.scope.SetName(tag)
	}
	return nil
}

// decodeHexID fills out from a hex string of exactly twice its length; an empty string leaves it zero.
func decodeHexID(s string, out []byte) error {
	if s == "" {
		return nil
	}
	if len(s) != 2*len(out) {
		return errors.New("id has the wrong length: " + strconv.Itoa(len(s)))
	}
	_, err := hex.Decode(out, []byte(s))
	return err
}

func takeObject(o *jsonObject, key string) (map[string]any, bool) {
	v, ok := o.take(key)
	if !ok {
		return nil, false
	}
	m, isObj := v.(map[string]any)
	return m, isObj
}
