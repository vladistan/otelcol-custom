// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"regexp"
	"strings"
)

var cronShape = regexp.MustCompile(`^cron: (.*)$`)

// cronBranch takes the tag of a "cron: ..." body as the service and strips it: journald names the
// logger program in the resource, which is wrong for these lines. A journald record of the identifier
// CRON with no tag gets the service and keeps its text.
type cronBranch struct{}

func newCronBranch() *cronBranch { return &cronBranch{} }

func (*cronBranch) name() string { return "cron" }

func (*cronBranch) claims(r *rec) bool {
	if strings.HasPrefix(r.body, "cron: ") && cronShape.MatchString(r.body) {
		return true
	}
	return r.body != "" && journalIdent(r.lr) == "CRON"
}

func (*cronBranch) parse(r *rec) error {
	text := r.body
	if m := cronShape.FindStringSubmatch(r.body); m != nil {
		text = m[1]
	}
	keepOriginal(r.lr.Attributes(), r.body)
	r.res.Attributes().PutStr("service.name", "cron")
	r.lr.Body().SetStr(text)
	return nil
}
