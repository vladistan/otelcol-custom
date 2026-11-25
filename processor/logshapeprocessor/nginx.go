// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/nginx"
	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/httpaccess"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

type nginxBranch struct{ cfg NginxConfig }

func newNginxBranch(cfg NginxConfig) *nginxBranch { return &nginxBranch{cfg: cfg} }

func (*nginxBranch) name() string { return "nginx" }

// text is the source attribute when set and present, else the body.
func (b *nginxBranch) text(r *rec) string {
	if b.cfg.SourceAttribute != "" {
		if v, ok := r.lr.Attributes().Get(b.cfg.SourceAttribute); ok && v.Str() != "" {
			return v.Str()
		}
	}
	return r.body
}

// claims parses the line and keeps the result for parse; a line with no bracket for the time is no access line.
func (b *nginxBranch) claims(r *rec) bool {
	text := b.text(r)
	if !strings.Contains(text, "[") {
		return false
	}
	info := nginx.ParseNginxLog(text)
	if info != nil {
		r.memo = info
	}
	return info != nil
}

func (b *nginxBranch) parse(r *rec) error {
	text := b.text(r)
	info := memoOf[nginx.NginxLogInfo](r)
	if info == nil {
		info = nginx.ParseNginxLog(text)
	}
	if info == nil {
		return errors.New("not a combined access line")
	}
	a := r.lr.Attributes()
	a.PutStr(originalKey, text)

	if b.cfg.SetServiceName {
		if _, ok := r.res.Attributes().Get(serviceKey); !ok {
			r.res.Attributes().PutStr(serviceKey, b.cfg.ServiceName)
		}
	}
	client := info.ClientAddr
	if forwarded := httpaccess.FirstForwarded(info.ForwardedFor); forwarded != "" {
		client = forwarded
		a.PutStr("network.peer.address", info.ClientAddr)
	}
	putNonEmpty(a, "client.address", client)
	putNonEmpty(a, "http.request.method", info.Method)
	putNonEmpty(a, "url.path", info.Path)
	putNonEmpty(a, "url.query", info.Query)
	if info.Protocol != "" {
		a.PutStr("network.protocol.name", "http")
		if len(info.Protocol) > 5 {
			a.PutStr("network.protocol.version", info.Protocol[5:])
		}
	}
	if info.StatusCode > 0 {
		a.PutInt("http.response.status_code", int64(info.StatusCode))
	}
	if info.BodyBytes >= 0 {
		a.PutInt("http.response.body.size", info.BodyBytes)
	}
	putNonEmpty(a, "http.request.header.referer", info.Referer)
	putUserAgent(a, info.UserAgent)
	putNonEmpty(a, "user.name", info.RemoteUser)

	if level, ok := severity.FromHTTPStatus(info.StatusCode); ok {
		r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
		r.lr.SetSeverityText(level.Text)
	}
	if b.cfg.ParseTimestamp && !info.Timestamp.IsZero() {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Timestamp))
	}
	r.lr.Body().SetStr(body.HTTP(body.HTTPLine{
		Method: info.Method, Path: info.Path, Query: info.Query, Status: info.StatusCode, SizeBytes: info.BodyBytes,
	}))
	return nil
}

func putUserAgent(a pcommon.Map, agent string) {
	if agent == "" {
		return
	}
	a.PutStr("user_agent.original", agent)
	if ua := nginx.ParseUserAgent(agent); ua != nil {
		putNonEmpty(a, "user_agent.name", ua.Name)
		putNonEmpty(a, "user_agent.version", ua.Version)
		putNonEmpty(a, "user_agent.os.name", ua.OSName)
		putNonEmpty(a, "vstan.nginx.device_type", ua.Device)
	}
}
