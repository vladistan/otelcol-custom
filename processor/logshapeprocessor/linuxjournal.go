// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const journalIdentifier = "SYSLOG_IDENTIFIER"

// journalIdent is the syslog identifier of a journald map body, or "" for any other body.
func journalIdent(lr plog.LogRecord) string {
	if lr.Body().Type() != pcommon.ValueTypeMap {
		return ""
	}
	v, ok := lr.Body().Map().Get(journalIdentifier)
	if !ok || v.Type() != pcommon.ValueTypeStr {
		return ""
	}
	return v.Str()
}

// journalPID is the _PID field of a journald map body as an integer: the receiver writes it as text.
func journalPID(lr plog.LogRecord) (int64, bool) {
	if lr.Body().Type() != pcommon.ValueTypeMap {
		return 0, false
	}
	v, ok := lr.Body().Map().Get("_PID")
	if !ok {
		return 0, false
	}
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return v.Int(), true
	case pcommon.ValueTypeStr:
		n, err := strconv.ParseInt(v.Str(), 10, 64)
		return n, err == nil
	}
	return 0, false
}

var (
	sshdPidPrefix = regexp.MustCompile(`^sshd(?:\[\d+\])?: `)
	sshdUser      = regexp.MustCompile(`(?:for user |for invalid user |for |from user |(?i:invalid user ))([^\s(]+)`)
	sshdFrom      = regexp.MustCompile(`\bfrom (?:user \S+ )?(\S+)(?: port (\d+))?`)
	sshdBy        = regexp.MustCompile(`\bby (\S+) port (\d+)`)
	sshdWarn      = []string{"Failed ", "refused connect", "Invalid user", "authentication failure"}
	sshdInfo      = []string{"Accepted ", "session opened", "session closed"}
)

// sshdBranch shapes sshd records of a journald map body: the pid prefix goes, the user and the client
// come from the line when it carries them, and a failed or refused login is a warning.
type sshdBranch struct{}

func newSSHDBranch() *sshdBranch { return &sshdBranch{} }

func (*sshdBranch) name() string { return "sshd" }

func (*sshdBranch) claims(r *rec) bool { return journalIdent(r.lr) == "sshd" }

func (*sshdBranch) parse(r *rec) error {
	if r.body == "" {
		return errors.New("an empty sshd line")
	}
	text := sshdPidPrefix.ReplaceAllString(r.body, "")
	a := r.lr.Attributes()
	keepOriginal(a, r.body)
	if m := sshdUser.FindStringSubmatch(text); m != nil {
		a.PutStr("user.name", m[1])
	}
	m := sshdFrom.FindStringSubmatch(text)
	if m == nil {
		m = sshdBy.FindStringSubmatch(text)
	}
	if m != nil {
		a.PutStr("client.address", m[1])
		if port, err := strconv.ParseInt(m[2], 10, 64); err == nil {
			a.PutInt("client.port", port)
		}
	}
	switch {
	case containsAny(text, sshdWarn):
		applyLevel(r.lr, wordLevel("warn"))
	case containsAny(text, sshdInfo):
		applyLevel(r.lr, wordLevel("info"))
	}
	r.res.Attributes().PutStr("service.name", "sshd")
	r.lr.Body().SetStr(text)
	return nil
}

var systemdFailure = []string{"Failed to ", "Failed with result", "entered failed state"}

// systemdBranch shapes systemd records of a journald map body: the text stays, the service is systemd and
// a unit failure is an error.
type systemdBranch struct{}

func newSystemdBranch() *systemdBranch { return &systemdBranch{} }

func (*systemdBranch) name() string { return "systemd" }

func (*systemdBranch) claims(r *rec) bool { return journalIdent(r.lr) == "systemd" }

func (*systemdBranch) parse(r *rec) error {
	if r.body == "" {
		return errors.New("an empty systemd line")
	}
	keepOriginal(r.lr.Attributes(), r.body)
	if containsAny(r.body, systemdFailure) {
		applyLevel(r.lr, wordLevel("error"))
	}
	r.res.Attributes().PutStr("service.name", "systemd")
	r.lr.Body().SetStr(r.body)
	return nil
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
