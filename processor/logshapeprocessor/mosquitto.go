// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// mosquittoShape needs a known mosquitto phrase after the time, so a free text that starts with a time is left alone.
var mosquittoShape = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}): ((?:New connection from|New client connected from|Client \S+ (?:disconnected|closed its connection|has exceeded)|Connecting bridge|Bridge |Socket error|mosquitto version|Config loaded|Opening ).*)$`)

// mosquittoBranch shapes Mosquitto lines: a UTC time without a zone and a message with no level.
type mosquittoBranch struct{}

func newMosquittoBranch() *mosquittoBranch { return &mosquittoBranch{} }

func (*mosquittoBranch) name() string { return "mosquitto" }

func (*mosquittoBranch) claims(r *rec) bool {
	return len(r.body) > 20 && r.body[10] == 'T' && r.body[19] == ':' && mosquittoShape.MatchString(r.body)
}

func (*mosquittoBranch) parse(r *rec) error {
	m := mosquittoShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a mosquitto line")
	}
	ts, err := time.Parse("2006-01-02T15:04:05", m[1])
	if err != nil {
		return err
	}
	r.lr.Attributes().PutStr("log.record.original", r.body)
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	r.lr.Body().SetStr(m[2])
	return nil
}
