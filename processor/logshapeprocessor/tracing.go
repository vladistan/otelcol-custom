// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"sort"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// tracingBranch shapes Rust tracing JSON lines: timestamp, level, fields.message and target.
type tracingBranch struct{}

func newTracingBranch() *tracingBranch { return &tracingBranch{} }

func (*tracingBranch) name() string { return "tracing" }

func (*tracingBranch) claims(r *rec) bool {
	s := strings.TrimSpace(r.body)
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, `"fields"`) || !strings.Contains(s, `"target"`) || !strings.Contains(s, `"level"`) {
		return false
	}
	o, err := parseJSONObject(s)
	if err != nil {
		return false
	}
	_, hasFields := o.object("fields")
	return hasFields && o.str("level") != "" && o.str("target") != ""
}

func (*tracingBranch) parse(r *rec) error {
	o, err := parseJSONObject(strings.TrimSpace(r.body))
	if err != nil {
		return err
	}
	fields, _ := o.object("fields")
	text, _ := fields["message"].(string)
	if text == "" {
		return errors.New("tracing line has no fields.message")
	}
	o.take("fields")
	delete(fields, "message")
	var ts pcommon.Timestamp
	if raw := o.takeStr("timestamp"); raw != "" {
		t, err := parseTime(raw)
		if err != nil {
			return err
		}
		ts = pcommon.NewTimestampFromTime(t)
	}
	level, _ := severity.FromSource(severity.Word, o.takeStr("level"))
	target := o.takeStr("target")

	a := r.lr.Attributes()
	putOriginal(a, r.body)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		putValue(a, "vstan.tracing."+k, normalizeJSON(fields[k]))
	}
	o.putAttrs(a, "vstan.tracing.")
	if ts != 0 {
		r.lr.SetTimestamp(ts)
	}
	setSeverity(r.lr, level)
	r.lr.Body().SetStr(text)
	r.scope.SetName(target)
	return nil
}
