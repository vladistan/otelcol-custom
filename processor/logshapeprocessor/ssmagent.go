// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore ssmagent

package logshapeprocessor

import (
	"errors"
	"regexp"
)

var ssmShape = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{4}) (ERROR|WARN|INFO|DEBUG|TRACE) (\S+) (.*)$`)

// ssmAgentBranch shapes amazon-ssm-agent lines: "time LEVEL Component text". The time has no zone,
// so the record time stays as it came; the component is the scope name.
type ssmAgentBranch struct{}

func newSSMAgentBranch() *ssmAgentBranch { return &ssmAgentBranch{} }

func (*ssmAgentBranch) name() string { return "ssmagent" }

func (*ssmAgentBranch) claims(r *rec) bool {
	return len(r.body) > 24 && r.body[4] == '-' && r.body[10] == ' ' && ssmShape.MatchString(r.body)
}

func (*ssmAgentBranch) parse(r *rec) error {
	m := ssmShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not an amazon-ssm-agent line")
	}
	r.lr.Attributes().PutStr("log.record.original", r.body)
	applyLevel(r.lr, wordLevel(m[2]))
	r.lr.Body().SetStr(m[4])
	r.scope.SetName(m[3])
	return nil
}
