// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"math"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/sentry"
	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

type sentryBranch struct{ cfg SentryConfig }

func newSentryBranch(cfg SentryConfig) *sentryBranch { return &sentryBranch{cfg: cfg} }

func (*sentryBranch) name() string { return "sentry" }

// text is the source attribute when set and present, else the body.
func (b *sentryBranch) text(r *rec) string {
	if b.cfg.SourceAttribute != "" {
		if v, ok := r.lr.Attributes().Get(b.cfg.SourceAttribute); ok && v.Str() != "" {
			return v.Str()
		}
	}
	return r.body
}

// claims parses the line and keeps the result for parse.
func (b *sentryBranch) claims(r *rec) bool {
	text := b.text(r)
	if !sentry.IsSentryLog(text) {
		return false
	}
	info := sentry.ParseSentryLog(text)
	if info != nil {
		r.memo = info
	}
	return info != nil
}

func (b *sentryBranch) parse(r *rec) error {
	text := b.text(r)
	info := memoOf[sentry.SentryLogInfo](r)
	if info == nil {
		info = sentry.ParseSentryLog(text)
	}
	if info == nil {
		return errors.New("not a Sentry log line")
	}
	a := r.lr.Attributes()
	if b.cfg.PreserveOriginal {
		a.PutStr(originalKey, text)
	}
	if full, ok := a.Get("full_message"); ok {
		a.PutStr(originalKey, full.Str())
		a.Remove("full_message")
	}
	if b.cfg.SetServiceName {
		if _, ok := r.res.Attributes().Get(serviceKey); !ok {
			r.res.Attributes().PutStr(serviceKey, b.cfg.ServiceName)
		}
	}
	r.scope.SetName(info.Logger)
	putNonEmpty(a, "vstan.sentry.message_type", info.MessageType)

	level, hasLevel := severity.FromSource(severity.Word, info.Level)
	status, hasStatus := atoiOK(info.Response)
	if b.cfg.ExtractHTTPFields {
		putNonEmpty(a, "http.request.method", info.Method)
		putNonEmpty(a, "url.path", info.Path)
		if hasStatus {
			a.PutInt("http.response.status_code", int64(status))
		}
		putNonEmpty(a, "client.address", info.CallerIP)
		putNonEmpty(a, "user_agent.original", info.UserAgent)
		putNonEmpty(a, "vstan.http.request.duration", info.Duration)
		putNonEmpty(a, "vstan.sentry.view", info.View)
	}
	if hasStatus {
		if l, ok := severity.FromHTTPStatus(status); ok {
			level, hasLevel = l, true
		}
	}
	if hasLevel {
		r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
		r.lr.SetSeverityText(level.Text)
	}
	if b.cfg.ParseTimestamp {
		b.setTime(r.lr, info.Timestamp)
	}
	r.lr.Body().SetStr(b.bodyOf(info, status, hasStatus))
	return nil
}

// bodyOf is the shared HTTP body for an access line, else the message of the parser.
func (b *sentryBranch) bodyOf(info *sentry.SentryLogInfo, status int, hasStatus bool) string {
	if !hasStatus || info.Method == "" || info.Path == "" {
		return info.Message
	}
	line := body.HTTPLine{Method: info.Method, Path: info.Path, Status: status}
	if secs, err := strconv.ParseFloat(info.Duration, 64); err == nil {
		line.DurationMicros, line.HasDuration = int64(math.Round(secs*1e6)), true
	}
	return body.HTTP(line)
}

// setTime puts the time of day of the line on the date of the record. A line time more than an hour
// after the record belongs to the day before. A record with no time gives no date, so nothing is set.
func (*sentryBranch) setTime(lr plog.LogRecord, clock time.Time) {
	ref := lr.Timestamp()
	if ref == 0 || clock.IsZero() {
		return
	}
	day := ref.AsTime().UTC()
	t := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
	if t.After(day.Add(time.Hour)) {
		t = t.AddDate(0, 0, -1)
	}
	lr.SetTimestamp(pcommon.NewTimestampFromTime(t))
}

func atoiOK(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}
