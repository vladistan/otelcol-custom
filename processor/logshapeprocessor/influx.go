// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore vstan

import (
	"errors"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/httpaccess"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

const influxPrefix = "[httpd] "

// influxBranch shapes InfluxDB httpd access lines.
type influxBranch struct{}

func newInfluxBranch() *influxBranch { return &influxBranch{} }

func (*influxBranch) name() string { return "influx" }

func (*influxBranch) claims(r *rec) bool { return strings.HasPrefix(r.body, influxPrefix) }

func (*influxBranch) parse(r *rec) error {
	e, ok := httpaccess.ParseInflux(r.body)
	if !ok {
		return errors.New("not an influx httpd access line")
	}
	a := r.lr.Attributes()
	putAccess(a, e)
	if e.RequestID != "" {
		a.PutStr("vstan.influx.request_id", e.RequestID)
	}
	if r.lr.Timestamp() == 0 {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(e.Time))
	}
	setHTTPSeverity(r.lr, e.Status)
	// An earlier stage may already hold the raw line; keep the first one.
	if _, ok := a.Get("log.record.original"); !ok {
		a.PutStr("log.record.original", r.body)
	}
	r.lr.Body().SetStr(e.Body())
	return nil
}

// putAccess writes the attributes every HTTP access format shares.
func putAccess(a pcommon.Map, e httpaccess.Entry) {
	a.PutStr("http.request.method", e.Method)
	a.PutStr("url.path", e.Path)
	if e.Query != "" {
		a.PutStr("url.query", e.Query)
	}
	a.PutInt("http.response.status_code", int64(e.Status))
	if e.SizeBytes > 0 {
		a.PutInt("http.response.body.size", e.SizeBytes)
	}
	a.PutStr("client.address", e.ClientAddress)
	if e.UserAgent != "" {
		a.PutStr("user_agent.original", e.UserAgent)
	}
	if e.User != "" {
		a.PutStr("user.name", e.User)
	}
}

// setHTTPSeverity leaves the severity untouched when the status has no class.
func setHTTPSeverity(lr plog.LogRecord, status int) {
	if l, ok := severity.FromHTTPStatus(status); ok {
		lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
		lr.SetSeverityText(l.Text)
	}
}
