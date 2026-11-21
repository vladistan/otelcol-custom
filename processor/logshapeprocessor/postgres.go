// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/postgres"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

type postgresBranch struct{ cfg PostgresConfig }

func newPostgresBranch(cfg PostgresConfig) *postgresBranch { return &postgresBranch{cfg: cfg} }

func (*postgresBranch) name() string { return "postgres" }

// text is the body, or the full_message attribute when the body is empty.
func (*postgresBranch) text(r *rec) (string, bool) {
	if r.body != "" {
		return r.body, false
	}
	if v, ok := r.lr.Attributes().Get("full_message"); ok {
		return v.Str(), true
	}
	return "", false
}

func (b *postgresBranch) claims(r *rec) bool {
	text, _ := b.text(r)
	return text != "" && postgres.IsPostgresLog(text)
}

func (b *postgresBranch) parse(r *rec) error {
	text, fromAttr := b.text(r)
	info := postgres.ParsePostgresLog(text)
	if info == nil {
		return errors.New("not a main PostgreSQL log line")
	}
	a := r.lr.Attributes()
	a.PutStr(originalKey, text)
	if fromAttr {
		a.Remove("full_message")
	}
	putInt(a, "process.pid", info.PID)
	if b.cfg.ExtractDetails {
		putNonEmpty(a, "vstan.postgres.detail", info.Detail)
		putNonEmpty(a, "vstan.postgres.statement", info.Statement)
		putNonEmpty(a, "vstan.postgres.hint", info.Hint)
		putNonEmpty(a, "vstan.postgres.context", info.Context)
	}
	if b.cfg.AdjustSeverity {
		if level, ok := severity.FromSource(severity.Postgres, info.Level); ok {
			r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
			r.lr.SetSeverityText(level.Text)
		}
	}
	if t, ok := postgres.LineTime(info.Timestamp, info.Zone); ok {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(t))
	}
	if b.cfg.RewriteMessages && info.Message != "" {
		r.lr.Body().SetStr(postgres.BuildCleanedMessage(info))
	} else if fromAttr {
		r.lr.Body().SetStr(text)
	}
	return nil
}
