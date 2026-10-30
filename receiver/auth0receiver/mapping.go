// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

type eventDetail struct {
	LogID string    `json:"log_id"`
	Data  *logEvent `json:"data"`
}

type logEvent struct {
	Date        string `json:"date"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Connection  string `json:"connection"`
	ClientName  string `json:"client_name"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	LogID       string `json:"log_id"`
}

// mapEnvelope turns one envelope into a single log record.
func mapEnvelope(env envelope) (plog.Logs, error) {
	var detail eventDetail
	if err := json.Unmarshal(env.Detail, &detail); err != nil {
		return plog.Logs{}, err
	}
	if detail.Data == nil {
		return plog.Logs{}, errors.New("detail has no data")
	}
	ev := detail.Data

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	res := rl.Resource().Attributes()
	res.PutStr("service.name", "auth0")
	putNonEmpty(res, "cloud.account.id", env.Account)
	putNonEmpty(res, "cloud.region", env.Region)

	rec := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	rec.SetTimestamp(pcommon.NewTimestampFromTime(eventTime(ev.Date, env.Time)))
	rec.Body().SetStr(eventBody(ev))
	sev := severityFor(ev.Type)
	rec.SetSeverityNumber(sev)
	rec.SetSeverityText(sev.String())

	logID := detail.LogID
	if logID == "" {
		logID = ev.LogID
	}
	attrs := rec.Attributes()
	attrs.PutStr("log.source", "auth0")
	attrs.PutStr("event.category", "authentication")
	putNonEmpty(attrs, "auth0.type", ev.Type)
	putNonEmpty(attrs, "auth0.log_id", logID)
	putNonEmpty(attrs, "event.id", logID)
	putNonEmpty(attrs, "auth0.client_name", ev.ClientName)
	putNonEmpty(attrs, "auth0.connection", ev.Connection)
	putNonEmpty(attrs, "user.id", ev.UserID)
	putNonEmpty(attrs, "user.name", ev.UserName)
	putNonEmpty(attrs, "client.address", ev.IP)
	putNonEmpty(attrs, "user_agent.original", ev.UserAgent)
	attrs.PutStr("original_message", string(env.Detail))
	return logs, nil
}

func putNonEmpty(m pcommon.Map, key, value string) {
	if value != "" {
		m.PutStr(key, value)
	}
}

// eventTime prefers the event's own date over the envelope time.
func eventTime(date, envelopeTime string) time.Time {
	for _, s := range []string{date, envelopeTime} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func eventBody(ev *logEvent) string {
	if ev.Description != "" {
		return ev.Description
	}
	return "auth0 " + ev.Type
}

// severityFor maps an Auth0 type code: f* is ERROR, w* is WARN, the rest INFO.
func severityFor(code string) plog.SeverityNumber {
	switch {
	case strings.HasPrefix(code, "f"):
		return plog.SeverityNumberError
	case strings.HasPrefix(code, "w"):
		return plog.SeverityNumberWarn
	default:
		return plog.SeverityNumberInfo
	}
}
