// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// mongodBranch shapes mongod structured lines: t.$date, s, c, id, ctx, msg and attr.
type mongodBranch struct{}

func newMongodBranch() *mongodBranch { return &mongodBranch{} }

func (*mongodBranch) name() string { return "mongod" }

func (*mongodBranch) claims(r *rec) bool {
	s := strings.TrimSpace(r.body)
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, `"$date"`) || !strings.Contains(s, `"msg"`) {
		return false
	}
	o, err := parseJSONObject(s)
	if err != nil {
		return false
	}
	t, hasT := o.object("t")
	_, hasDate := t["$date"].(string)
	return hasT && hasDate && o.str("s") != "" && o.has("msg")
}

func (*mongodBranch) parse(r *rec) error {
	o, err := parseJSONObject(strings.TrimSpace(r.body))
	if err != nil {
		return err
	}
	t, _ := o.object("t")
	date, _ := t["$date"].(string)
	ts, err := parseTime(date)
	if err != nil {
		return err
	}
	msg, isStr := o.vals["msg"].(string)
	if !isStr {
		return errors.New("mongod message is not a string")
	}
	level, _ := severity.FromSource(severity.Mongod, o.takeStr("s"))
	component := o.takeStr("c")
	attr, _ := takeObject(o, "attr")
	o.take("t")
	o.take("msg")

	a := r.lr.Attributes()
	putOriginal(a, r.body)
	if id, ok := o.take("id"); ok {
		putValue(a, "vstan.mongod.id", id)
	}
	if ctx := o.takeStr("ctx"); ctx != "" {
		a.PutStr("vstan.mongod.ctx", ctx)
	}
	o.putAttrs(a, "vstan.mongod.")
	pairs := putMongodAttr(a, attr)

	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	setSeverity(r.lr, level)
	r.lr.Body().SetStr(body.Compose("", msg, pairs...))
	if component != "" {
		r.scope.SetName(component)
	}
	return nil
}

// putMongodAttr writes the attr object as attributes and returns the body pairs: the principal, the
// remote address and the connection count.
func putMongodAttr(a pcommon.Map, attr map[string]any) []body.Pair {
	var principal, remote, count string
	keys := make([]string, 0, len(attr))
	for k := range attr {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := normalizeJSON(attr[k])
		switch k {
		case "remote":
			remote, _ = v.(string)
			if host, port, err := net.SplitHostPort(remote); err == nil {
				a.PutStr("client.address", host)
				if n, err := strconv.ParseInt(port, 10, 64); err == nil {
					a.PutInt("client.port", n)
				}
			} else {
				putValue(a, "vstan.mongod.remote", v)
			}
		case "principalName":
			principal, _ = v.(string)
			putNonEmpty(a, "user.name", principal)
		case "connectionCount":
			count = scalarText(v)
			putValue(a, "vstan.mongod.connection_count", v)
		default:
			putValue(a, "vstan.mongod."+snakeCase(k), v)
		}
	}
	return []body.Pair{{Key: "principal", Value: principal}, {Key: "remote", Value: remote}, {Key: "connectionCount", Value: count}}
}

// snakeCase turns connectionId into connection_id.
func snakeCase(s string) string {
	var b strings.Builder
	for i, c := range s {
		if unicode.IsUpper(c) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}
