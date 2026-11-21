// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

var stallShape = regexp.MustCompile(`^(?:(\S+) (?:stdout|stderr) [FP] )?published (\w+)=(\S+)$`)

// stallCheckBranch shapes the stall-check cron job line "published Name=value", with or without the
// container runtime prefix.
type stallCheckBranch struct{}

func newStallCheckBranch() *stallCheckBranch { return &stallCheckBranch{} }

func (*stallCheckBranch) name() string { return "stallcheck" }

func (*stallCheckBranch) claims(r *rec) bool {
	return strings.Contains(r.body, "published ") && stallShape.MatchString(r.body)
}

func (*stallCheckBranch) parse(r *rec) error {
	m := stallShape.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a stall-check line")
	}
	var ts time.Time
	if m[1] != "" {
		var err error
		if ts, err = time.Parse(time.RFC3339Nano, m[1]); err != nil {
			return err
		}
	}
	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	putScalar(a, "vstan.stallcheck."+underscoreCase(m[2]), m[3])
	if !ts.IsZero() {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	}
	applyLevel(r.lr, wordLevel("info"))
	r.lr.Body().SetStr("published " + m[2] + "=" + m[3])
	return nil
}
