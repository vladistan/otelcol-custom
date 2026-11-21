// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"
)

var cronShape = regexp.MustCompile(`^cron: (.*)$`)

// cronBranch takes the tag of a "cron: ..." body as the service and strips it: journald names the
// logger program in the resource, which is wrong for these lines.
type cronBranch struct{}

func newCronBranch() *cronBranch { return &cronBranch{} }

func (*cronBranch) name() string { return "cron" }

func (*cronBranch) claims(r *rec) bool {
	return strings.HasPrefix(r.body, "cron: ") && cronShape.MatchString(r.body)
}

func (*cronBranch) parse(r *rec) error {
	m := cronShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a cron line")
	}
	r.lr.Attributes().PutStr("log.record.original", r.body)
	r.res.Attributes().PutStr("service.name", "cron")
	r.lr.Body().SetStr(m[1])
	return nil
}
