// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore upid vzdump vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Inline fixtures: the task ids and most texts follow the layout of Proxmox task logs with made-up
// values. Captured lines are in testdata/samples-keyed/pbs.jsonl (records 1 to 3); pve.jsonl is invented.
const (
	// A task id as it appears in the file name: node, pid, pstart, task id, start, worker, id and user.
	pbsGC   = `UPID:backup:0001A2B3:0C0D0E0F:65F1A2B3:65F1A2C0:garbage_collection:aws\x2dapps:root@pam:`
	pbsLog  = `UPID:backup:0001A2B4:0C0D0E10:65F1A2B4:65F1A2C1:logrotate::root@pam:`
	pveDump = "/var/log/pve/tasks/A/UPID:s01:00001A2B:0C0D0E0F:65F1A2B3:65F1A2C0:vzdump::root@pam:"
)

func pbsInput(upid, body string) input {
	return input{body: body, attrs: map[string]any{"log.file.name": upid}}
}

func TestPBS_ProgressLine_GetsTheDatastorePrefixAndTheServiceFromTheTaskType(t *testing.T) {
	lr, res := runW4(t, pbsInput(pbsGC, "2026-11-20T00:02:45+01:00: processed 99% (583303 chunks)"))

	w4Body(t, lr, "[aws-apps] processed 99% (583303 chunks)")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	w4Time(t, lr, time.Date(2026, 11, 19, 23, 2, 45, 0, time.UTC))
	a := lr.Attributes()
	if attrStr(t, a, "vstan.pbs.task") != "garbage_collection" || attrStr(t, a, "vstan.pbs.datastore") != "aws-apps" || attrStr(t, a, "user.name") != "root@pam" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	if attrStr(t, res.Attributes(), "service.name") != "garbage_collection" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestPBS_TaskResultLines_MapToInfoWarnAndError(t *testing.T) {
	cases := map[string]plog.SeverityNumber{
		"TASK OK":                           plog.SeverityNumberInfo,
		"TASK WARNINGS: 2":                  plog.SeverityNumberWarn,
		"TASK ERROR: connection error: eof": plog.SeverityNumberError,
	}
	for text, num := range cases {
		lr, _ := runW4(t, pbsInput(pbsGC, "2026-11-20T04:00:46+01:00: "+text))
		if lr.SeverityNumber() != num {
			t.Fatalf("%q -> %d, want %d", text, lr.SeverityNumber(), num)
		}
	}
}

func TestPBS_TaskWithoutADatastore_HasNoPrefix(t *testing.T) {
	lr, _ := runW4(t, pbsInput(pbsLog, "2026-11-20T04:00:46+01:00: TASK OK"))

	w4Body(t, lr, "TASK OK")
	if _, ok := lr.Attributes().Get("vstan.pbs.datastore"); ok {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestPBS_TimeLineWithoutATaskFile_IsNotClaimed(t *testing.T) {
	const line = "2026-11-20T04:00:46+01:00: TASK OK"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}

func TestPBS_ImpossibleTime_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	in := pbsInput(pbsGC, "2026-13-45T25:61:61+01:00: TASK OK")

	lr, _ := h.run(in)

	if lr.Body().Str() != in.body || lr.Attributes().Len() != 1 {
		t.Fatalf("record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	w4Failed(t, h, "pbs")
}

func TestPVE_WholeTaskLog_TakesTheSeverityFromTheLastLineAndKeepsTheBody(t *testing.T) {
	cases := map[string]plog.SeverityNumber{
		"TASK OK": plog.SeverityNumberInfo, "TASK WARNINGS: 1": plog.SeverityNumberWarn, "TASK ERROR: broken pipe": plog.SeverityNumberError,
	}
	for last, num := range cases {
		body := "INFO: starting new backup job\nINFO:  17% (14.9 GiB of 87.8 GiB) in 54s\n" + last
		lr, _ := runW4(t, input{body: body, attrs: map[string]any{"log.file.path": pveDump}})

		w4Body(t, lr, body)
		if lr.SeverityNumber() != num {
			t.Fatalf("%q -> %d, want %d", last, lr.SeverityNumber(), num)
		}
		a := lr.Attributes()
		if attrStr(t, a, "vstan.pve.task") != "vzdump" || attrStr(t, a, "user.name") != "root@pam" {
			t.Fatalf("attributes = %v", a.AsRaw())
		}
	}
}

func TestPVE_TaskLogWithoutAResultLine_IsNotClaimed(t *testing.T) {
	attrs := map[string]any{"log.file.path": pveDump}
	const body = "INFO: starting new backup job\nINFO:  17% (14.9 GiB of 87.8 GiB) in 54s"

	lr, _ := runW4(t, input{body: body, attrs: attrs})

	w4Untouched(t, lr, body, attrs)
}
