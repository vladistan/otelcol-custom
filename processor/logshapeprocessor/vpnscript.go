// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

var (
	vpnscriptLine = regexp.MustCompile(`(?s)^\[([^\]]+)\]\s*(.*)$`)
	// vpnscriptISO is the ISO form: a date, a "T" or a space, a clock with an optional fraction, and an
	// optional zone ("Z", "+01:00" or "+0100").
	vpnscriptISO   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2}(?:[.,]\d+)?)(Z|[+-]\d{2}:?\d{2})?$`)
	vpnscriptLevel = regexp.MustCompile(`(?i)\b(fatal|error|warning|warn)\b`)
)

type vpnscriptInfo struct {
	ts   time.Time
	text string
}

// parseVPNScriptTime reads the bracket: the ISO form, or the date command forms (UnixDate and ANSIC). No zone is
// UTC. A zone abbreviation of the date forms is read as UTC too, because it is not a fixed offset.
func parseVPNScriptTime(bracket string) (time.Time, bool) {
	if m := vpnscriptISO.FindStringSubmatch(bracket); m != nil {
		zone := m[3]
		switch {
		case zone == "":
			zone = "Z"
		case len(zone) == 5:
			zone = zone[:3] + ":" + zone[3:]
		}
		ts, err := time.Parse(time.RFC3339Nano, m[1]+"T"+strings.Replace(m[2], ",", ".", 1)+zone)
		return ts.UTC(), err == nil
	}
	for _, layout := range []string{time.UnixDate, time.ANSIC} {
		if ts, err := time.ParseInLocation(layout, bracket, time.UTC); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseVPNScript(body string) (*vpnscriptInfo, bool) {
	m := vpnscriptLine.FindStringSubmatch(strings.TrimRight(strings.TrimLeft(body, " \t"), "\r\n"))
	if m == nil {
		return nil, false
	}
	ts, ok := parseVPNScriptTime(m[1])
	if !ok {
		return nil, false
	}
	return &vpnscriptInfo{ts: ts, text: strings.TrimSpace(m[2])}, true
}

// vpnscriptBranch shapes "[timestamp] text" lines of a script. It is the one branch that defaults to INFO: the
// text sets another level only when it names one, and the highest named level wins.
type vpnscriptBranch struct{}

func newVPNScriptBranch() *vpnscriptBranch { return &vpnscriptBranch{} }

func (*vpnscriptBranch) name() string { return "vpnscript" }

func (*vpnscriptBranch) claims(r *rec) bool {
	info, ok := parseVPNScript(r.body)
	if ok {
		r.memo = info
	}
	return ok
}

func (*vpnscriptBranch) parse(r *rec) error {
	info := memoOf[vpnscriptInfo](r)
	if info == nil {
		var ok bool
		if info, ok = parseVPNScript(r.body); !ok {
			return errors.New("not a timestamped script line")
		}
	}
	level := wordLevel("info")
	for _, word := range vpnscriptLevel.FindAllString(info.text, -1) {
		if l := wordLevel(word); l.Number > level.Number {
			level = l
		}
	}
	r.lr.Attributes().PutStr(originalKey, r.body)
	applyLevel(r.lr, level)
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(info.ts))
	r.lr.Body().SetStr(info.text)
	return nil
}
