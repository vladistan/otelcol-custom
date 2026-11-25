// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"math"
	"net/url"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// awsELBBranch shapes AWS load balancer access records, which arrive with an empty body.
type awsELBBranch struct {
	aliases map[string]string
}

func newAWSELBBranch(aliases map[string]string) *awsELBBranch {
	return &awsELBBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*awsELBBranch) name() string { return "awselb" }

func (*awsELBBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "elb" }

func (b *awsELBBranch) parse(r *rec) error {
	a, res := r.lr.Attributes(), r.res.Attributes()
	host := lbString("url.domain", a)
	path := lbString("url.path", a)
	if full := lbString("url.full", a); full != "" && (host == "" || path == "") {
		fh, fp := lbSplitURL(full)
		if host == "" {
			host = fh
		}
		if path == "" {
			path = fp
		}
	}
	status, hasStatus := lbInt(a, "aws.elb.status.code", "http.response.status_code")
	line := body.HTTPLine{Method: lbString("http.request.method", a), Host: host, Path: path, Status: status}
	if secs, ok := lbNumber(a, "aws.elb.target_processing_time"); ok && secs >= 0 {
		line.DurationMicros, line.HasDuration = int64(math.Round(secs*1e6)), true
	}
	lbSetBody(r, body.HTTP(line))
	lbSetSeverity(r.lr, status, hasStatus)
	// The semconv names are what healthprobe and other samplers read; the aws.elb.status.code and url.full source attributes stay.
	if hasStatus {
		a.PutInt("http.response.status_code", int64(status))
	}
	if path != "" {
		a.PutStr("url.path", path)
	}

	if lb := principal.LoadBalancerName(lbString("cloud.resource_id", res, a)); lb != "" {
		res.PutStr("host.name", lb)
	}
	if tg := lbTargetGroup(lbString("aws.elb.target_group_arn", a)); tg != "" {
		res.PutStr("service.name", tg)
	}
	lbSetAccountName(res, a, b.aliases)
	return nil
}

// lbTargetGroup returns <name> of an ARN ending in targetgroup/<name>/<id>.
func lbTargetGroup(arn string) string {
	_, rest, ok := strings.Cut(arn, "targetgroup/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// lbString returns the first non-empty string value of key, looking in each map in order.
func lbString(key string, maps ...pcommon.Map) string {
	for _, m := range maps {
		if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeStr && v.Str() != "" {
			return v.Str()
		}
	}
	return ""
}

// lbNumber reads a number stored as a double, an int or a numeric string.
func lbNumber(m pcommon.Map, keys ...string) (float64, bool) {
	for _, k := range keys {
		v, ok := m.Get(k)
		if !ok {
			continue
		}
		switch v.Type() {
		case pcommon.ValueTypeInt:
			return float64(v.Int()), true
		case pcommon.ValueTypeDouble:
			return v.Double(), true
		case pcommon.ValueTypeStr:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v.Str()), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func lbInt(m pcommon.Map, keys ...string) (int, bool) {
	f, ok := lbNumber(m, keys...)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1e9 {
		return 0, false
	}
	return int(f), true
}

func lbSplitURL(raw string) (host, path string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	return u.Hostname(), u.Path
}

// lbSetBody replaces the body with the shaped text and keeps a non-empty original.
// An empty text leaves the record as it was.
func lbSetBody(r *rec, text string) {
	if text == "" {
		return
	}
	if r.body != "" && r.body != text {
		if _, ok := r.lr.Attributes().Get("log.record.original"); !ok {
			r.lr.Attributes().PutStr("log.record.original", r.body)
		}
	}
	r.lr.Body().SetStr(text)
}

// lbSetSeverity maps an HTTP status; without a status the record keeps its severity.
func lbSetSeverity(lr plog.LogRecord, status int, ok bool) {
	if !ok {
		return
	}
	if l, found := severity.FromHTTPStatus(status); found {
		lbApplyLevel(lr, l)
	}
}

func lbApplyLevel(lr plog.LogRecord, l severity.Level) {
	lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
	lr.SetSeverityText(l.Text)
}

// lbSetAccountName sets cloud.account.name only for an account id that has an alias.
func lbSetAccountName(res, a pcommon.Map, aliases map[string]string) {
	id := lbString("cloud.account.id", res, a)
	if id == "" {
		return
	}
	if alias := principal.AccountAlias(id, aliases); alias != id {
		res.PutStr("cloud.account.name", alias)
	}
}
