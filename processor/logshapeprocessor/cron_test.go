// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// INVENTED fixture: the "cron:" tag in the body follows the ledger text, the command is made up.
func TestCron_TaggedBody_UsesTheTagAsServiceAndStripsIt(t *testing.T) {
	const line = "cron: (root) CMD (/usr/local/bin/backup.sh)"

	lr, res := runW4(t, input{body: line, resource: map[string]any{"service.name": "logger"}})

	w4Body(t, lr, "(root) CMD (/usr/local/bin/backup.sh)")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	if attrStr(t, res.Attributes(), "service.name") != "cron" || attrStr(t, lr.Attributes(), "log.record.original") != line {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), lr.Attributes().AsRaw())
	}
}

func TestCron_OtherTags_AreNotClaimed(t *testing.T) {
	for _, line := range []string{"error: disk full", "cronjob: done", "crontab: no changes"} {
		lr, _ := runW4(t, input{body: line})

		w4Untouched(t, lr, line, map[string]any{})
	}
}
