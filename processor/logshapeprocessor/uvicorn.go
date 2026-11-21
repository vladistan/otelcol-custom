// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"regexp"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/uvicorn"
	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

var uvicornPrefix = regexp.MustCompile(`^(TRACE|DEBUG|INFO|WARNING|ERROR|CRITICAL):\s+(.*)$`)

type uvicornBranch struct{ cfg UvicornConfig }

func newUvicornBranch(cfg UvicornConfig) *uvicornBranch { return &uvicornBranch{cfg: cfg} }

func (*uvicornBranch) name() string { return "uvicorn" }

// uvicornMatch is the level and the text that claims found in the line.
type uvicornMatch struct{ parts []string }

// claims matches the prefix and keeps the groups for parse.
func (*uvicornBranch) claims(r *rec) bool {
	m := uvicornPrefix.FindStringSubmatch(r.body)
	if m != nil {
		r.memo = &uvicornMatch{parts: m}
	}
	return m != nil
}

// parse reads an access line with the uvicorn parser. Any other line with a level prefix gets the severity
// of the prefix and loses the prefix.
func (b *uvicornBranch) parse(r *rec) error {
	var m []string
	if kept := memoOf[uvicornMatch](r); kept != nil {
		m = kept.parts
	} else {
		m = uvicornPrefix.FindStringSubmatch(r.body)
	}
	if m == nil {
		return errors.New("not a uvicorn line")
	}
	level, _ := severity.FromSource(severity.Word, m[1])
	text := m[2]
	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)

	if info := uvicorn.ParseUvicornLog(r.body); info != nil {
		if b.cfg.ExtractHTTPFields {
			putNonEmpty(a, "client.address", info.ClientIP)
			if info.ClientPort != "" {
				putInt(a, "client.port", info.ClientPort)
			}
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
			putNonEmpty(a, "vstan.uvicorn.status_text", info.StatusText)
		}
		if b.cfg.AdjustSeverity {
			if l, ok := severity.FromHTTPStatus(info.StatusCode); ok {
				level = l
			}
		}
		if b.cfg.RewriteMessages {
			text = body.HTTP(body.HTTPLine{Method: info.Method, Path: info.Path, Query: info.Query, Status: info.StatusCode})
		}
	} else if !b.cfg.RewriteMessages {
		text = r.body
	}
	if b.cfg.AdjustSeverity && level.Number != 0 {
		r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
		r.lr.SetSeverityText(level.Text)
	}
	r.lr.Body().SetStr(text)
	return nil
}
