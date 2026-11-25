// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/principal"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// auditStr reads a string attribute; any other type reads as empty.
func auditStr(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

// auditFirstStr returns the first non-empty string among the keys of the record, then the resource.
func auditFirstStr(r *rec, keys ...string) string {
	for _, m := range []pcommon.Map{r.lr.Attributes(), r.res.Attributes()} {
		for _, k := range keys {
			if s := auditStr(m, k); s != "" {
				return s
			}
		}
	}
	return ""
}

// auditInt reads an integer attribute that may be stored as an int, a double or a numeric string.
func auditInt(m pcommon.Map, key string) (int64, bool) {
	v, ok := m.Get(key)
	if !ok {
		return 0, false
	}
	return auditNumber(v.AsRaw())
}

func auditNumber(raw any) (int64, bool) {
	switch n := raw.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// auditSetLevel writes a severity; an unset level leaves the record as it is.
func auditSetLevel(lr plog.LogRecord, l severity.Level) {
	if l.Number == 0 {
		return
	}
	lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
	lr.SetSeverityText(l.Text)
}

func auditWord(word string) severity.Level {
	l, _ := severity.FromSource(severity.Word, word)
	return l
}

// auditSetBody replaces the body only when the shaped text is not empty.
func auditSetBody(lr plog.LogRecord, text string) {
	if text != "" {
		lr.Body().SetStr(text)
	}
}

// auditHost sets host.name to the alias of the id, or the id itself, and
// cloud.account.name only when the id has an alias.
func auditHost(res pcommon.Resource, id string, aliases map[string]string, withAccountName bool) {
	if id == "" {
		return
	}
	a := res.Attributes()
	a.PutStr("host.name", principal.AccountAlias(id, aliases))
	if withAccountName && aliases[id] != "" {
		a.PutStr("cloud.account.name", aliases[id])
	}
}

// setAccountName sets cloud.account.name to the alias of a GCP project id, or to the id itself when
// it has no alias. An empty id writes nothing.
func setAccountName(attrs pcommon.Map, id string, aliases map[string]string) {
	if id == "" {
		return
	}
	if alias := aliases[id]; alias != "" {
		attrs.PutStr("cloud.account.name", alias)
		return
	}
	attrs.PutStr("cloud.account.name", id)
}

func auditService(res pcommon.Resource, short string) {
	if short != "" {
		res.Attributes().PutStr("service.name", short)
	}
}
