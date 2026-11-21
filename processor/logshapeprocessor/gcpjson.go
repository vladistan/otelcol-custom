// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

const (
	gcpOperation      = "logging.googleapis.com/operation"
	gcpSourceLocation = "logging.googleapis.com/sourceLocation"
)

// gcpJSONBranch shapes GCP structured lines: message, severity, timestamp and the logging fields.
type gcpJSONBranch struct{}

func newGCPJSONBranch() *gcpJSONBranch { return &gcpJSONBranch{} }

func (*gcpJSONBranch) name() string { return "gcpjson" }

func (*gcpJSONBranch) claims(r *rec) bool {
	s := strings.TrimSpace(r.body)
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, `"severity"`) || !strings.Contains(s, `"message"`) {
		return false
	}
	o, err := parseJSONObject(s)
	return err == nil && o.str("severity") != "" && o.has("message")
}

func (*gcpJSONBranch) parse(r *rec) error {
	o, err := parseJSONObject(strings.TrimSpace(r.body))
	if err != nil {
		return err
	}
	msg, isStr := o.vals["message"].(string)
	if !isStr {
		return errors.New("gcp line message is not a string")
	}
	o.take("message")
	var ts pcommon.Timestamp
	if raw := o.takeStr("timestamp"); raw != "" {
		t, err := parseTime(raw)
		if err != nil {
			return err
		}
		ts = pcommon.NewTimestampFromTime(t)
	}
	level, _ := severity.FromSource(severity.Word, o.takeStr("severity"))

	a := r.lr.Attributes()
	putOriginal(a, r.body)
	if v, ok := o.take(gcpOperation); ok {
		if s, isStr := v.(string); isStr {
			a.PutStr("code.function.name", strings.TrimSuffix(s, "()"))
		} else {
			putValue(a, "vstan.gcpjson.operation", v)
		}
	}
	if v, ok := o.take(gcpSourceLocation); ok {
		putSourceLocation(a, v)
	}
	o.putAttrs(a, "vstan.gcpjson.")
	if ts != 0 {
		r.lr.SetTimestamp(ts)
	}
	setSeverity(r.lr, level)
	r.lr.Body().SetStr(msg)
	return nil
}

// putSourceLocation reads a path string or the {file, line, function} object of the logging agent.
func putSourceLocation(a pcommon.Map, v any) {
	switch loc := v.(type) {
	case string:
		a.PutStr("code.file.path", loc)
	case map[string]any:
		if f, ok := loc["file"].(string); ok {
			a.PutStr("code.file.path", f)
		}
		switch n := loc["line"].(type) {
		case string:
			if line, err := strconv.ParseInt(n, 10, 64); err == nil {
				a.PutInt("code.line.number", line)
			}
		case int64:
			a.PutInt("code.line.number", n)
		}
		if fn, ok := loc["function"].(string); ok {
			a.PutStr("code.function.name", fn)
		}
	}
}
