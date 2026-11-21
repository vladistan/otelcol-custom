// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"sort"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/logrus"
	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/rename"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

type logrusBranch struct{ cfg LogrusConfig }

func newLogrusBranch(cfg LogrusConfig) *logrusBranch { return &logrusBranch{cfg: cfg} }

func (*logrusBranch) name() string { return "logrus" }

type logrusForm int

const (
	formJSON logrusForm = iota + 1
	formText
	formZapConsole
)

// logrusMemo is the form of the line that claims recognized.
type logrusMemo struct{ form logrusForm }

// formOf names the form of a body, in the order claims has always tested: JSON, text, zap console.
func formOf(text string) logrusForm {
	switch {
	case logrus.IsLogrusJSON(text):
		return formJSON
	case logrus.IsLogrusText(text):
		return formText
	case logrus.IsZapConsole(text):
		return formZapConsole
	}
	return 0
}

// claims a body that is a logrus or zap JSON line, a logrus text line or a zap console line, and keeps
// which one for parse.
func (*logrusBranch) claims(r *rec) bool {
	form := formOf(r.body)
	if form != 0 {
		r.memo = &logrusMemo{form: form}
	}
	return form != 0
}

func (b *logrusBranch) parse(r *rec) error {
	form := formZapConsole
	if kept := memoOf[logrusMemo](r); kept != nil {
		form = kept.form
	} else if f := formOf(r.body); f != 0 {
		form = f
	}
	var info *logrus.LogrusLogInfo
	switch form {
	case formJSON:
		info = logrus.ParseLogrusJSON(r.body)
	case formText:
		info = logrus.ParseLogrusText(r.body)
	default:
		info = logrus.ParseZapConsole(r.body)
	}
	if info == nil {
		return errors.New("not a logrus or zap line")
	}
	if b.cfg.ParseTimestamp && info.TimeBad {
		return errors.New("the line has a time that cannot be read")
	}

	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)
	if info.Caller != "" {
		file, line, hasLine := rename.FileLine(info.Caller)
		a.PutStr("code.file.path", file)
		if hasLine {
			a.PutInt("code.line.number", line)
		}
	}
	putNonEmpty(a, "vstan.k8s.controller", info.Controller)
	putNonEmpty(a, "exception.type", info.ErrorType)
	putNonEmpty(a, "exception.message", info.Error)
	putNonEmpty(a, "exception.stacktrace", info.Stacktrace)
	putFields(a, info.Fields)
	if info.Logger != "" {
		r.scope.SetName(info.Logger)
	}

	if b.cfg.AdjustSeverity {
		if level, ok := severity.FromSource(severity.Word, info.Level); ok {
			r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
			r.lr.SetSeverityText(level.Text)
		}
	}
	if b.cfg.ParseTimestamp && !info.Timestamp.IsZero() {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Timestamp))
	}
	if b.cfg.RewriteMessages && info.Message != "" {
		text := info.Message
		if info.Error != "" {
			text = joinError(text, info.Error)
		}
		r.lr.Body().SetStr(body.Compose(info.Module, text))
	}
	return nil
}

func putFields(a pcommon.Map, fields map[string]any) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := fields[k].(type) {
		case string:
			a.PutStr("vstan.logrus."+k, v)
		case int64:
			a.PutInt("vstan.logrus."+k, v)
		case float64:
			a.PutDouble("vstan.logrus."+k, v)
		case bool:
			a.PutBool("vstan.logrus."+k, v)
		}
	}
}

// joinError puts the error after the message with one colon: a message that already ends in a colon is
// followed by a space only.
func joinError(message, err string) string {
	if strings.HasSuffix(message, ":") {
		return message + " " + err
	}
	return message + ": " + err
}
