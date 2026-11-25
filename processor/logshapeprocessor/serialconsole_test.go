// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func serialInput(body string) input {
	return input{
		body: body,
		attrs: map[string]any{
			"cloud.resource_id": "serialconsole.googleapis.com/serial_port_1_output",
			"is_text_escaped":   true,
			"gcp.label.compute.googleapis.com/resource_name": "gk3-cluster-a-nap-w7v5",
		},
	}
}

// The bodies are the escaped text of the console: the backslashes are in the string.
const (
	serialOK     = `[\x1b[0;32m  OK  \x1b[0m] Started Daily apt download activities.\r\n`
	serialFailed = `[\x1b[0;1;31mFAILED\x1b[0m] Failed to start Docker Application Container Engine.\r\n`
	serialJob    = `[\x1b[0;31m*     \x1b[0m] A start job is running for Wait for Network (11s / 1min 30s)\r`
)

func TestSerialConsole_OKLine_IsUnescapedStrippedAndInfo(t *testing.T) {
	lr, res := runW4(t, serialInput(serialOK))

	w4Body(t, lr, "[  OK  ] Started Daily apt download activities.")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	if attrStr(t, res.Attributes(), "host.name") != "gk3-cluster-a-nap-w7v5" || attrStr(t, lr.Attributes(), "log.record.original") != serialOK {
		t.Fatalf("resource = %v attributes = %v", res.Attributes().AsRaw(), lr.Attributes().AsRaw())
	}
}

func TestSerialConsole_FailedLine_IsError(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialFailed))

	w4Body(t, lr, "[FAILED] Failed to start Docker Application Container Engine.")
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestSerialConsole_RunningJobLine_IsWarn(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialJob))

	w4Body(t, lr, "[*     ] A start job is running for Wait for Network (11s / 1min 30s)")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
}

func TestSerialConsole_KernelLine_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, serialInput(`[    1.234567] Linux version 6.1.0\r\n`))

	w4Body(t, lr, "[    1.234567] Linux version 6.1.0")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestSerialConsole_EscapedBytes_FormUtf8(t *testing.T) {
	lr, _ := runW4(t, serialInput(`Starting \xe2\x80\xa6\r\n`))

	w4Body(t, lr, "Starting …")
}

func TestSerialConsole_BareCRLFRecord_BecomesAnEmptyBody(t *testing.T) {
	lr, _ := runW4(t, serialInput(`\r\n`))

	w4Body(t, lr, "")
}

const (
	serialTS      = `2026-11-22T10:00:00.123456+0000 node-a `
	serialGcfsd   = serialTS + `gcfsd-v2[2957]: time="2026-11-22T10:00:00Z" level=info msg="mounted layer" layer=3` + `\r\n`
	serialNoMsg   = serialTS + `gcfsd-v2[2957]: time="2026-11-22T10:00:01Z" level=error layer=3 err=timeout` + `\r\n`
	serialUnknown = serialTS + `gcfsd-v2[2957]: time="2026-11-22T10:00:02Z" level=loud msg="odd level"` + `\r\n`
	serialNoPid   = serialTS + `gcfsd-v2: time="2026-11-22T10:00:03Z" level=info msg="no pid"` + `\r\n`
	serialNoTag   = serialTS + `kernel boot message` + `\r\n`
)

// serialSnapshot builds a containerd snapshot line with n sha256 label entries.
func serialSnapshot(n int) (line, labels string) {
	var entries []string
	for i := range n {
		entries = append(entries, fmt.Sprintf("containerd.io/snapshot/gcfs/layer.%d=sha256:%s", i, strings.Repeat("ab", 32)))
	}
	labels = strings.Join(entries, ",")
	line = serialTS + `containerd-gcfs-grpc[2997]: time="2026-11-22T10:00:04Z" level=warning msg="snapshot labels: ` + labels + `"` + `\r\n`
	return line, labels
}

// notSyslog fails when the syslog branch took the record: it marks every record it claims with log.source.
func notSyslog(t *testing.T, lr plog.LogRecord) {
	t.Helper()
	absent(t, lr.Attributes(), "log.source", "vstan.syslog.parse_error", "vstan.logrus.layer")
}

func TestSerialConsole_TaggedLine_GivesServiceAndIntegerPid(t *testing.T) {
	lr, res := runW4(t, serialInput(serialGcfsd))

	notSyslog(t, lr)
	attrInt(t, lr.Attributes(), "process.pid", 2957)
	if got := attrStr(t, res.Attributes(), "service.name"); got != "gcfsd-v2" {
		t.Fatalf("service.name = %q", got)
	}
	line, _ := serialSnapshot(5)
	lr, res = runW4(t, serialInput(line))

	notSyslog(t, lr)
	attrInt(t, lr.Attributes(), "process.pid", 2997)
	if got := attrStr(t, res.Attributes(), "service.name"); got != "containerd-gcfs-grpc" {
		t.Fatalf("service.name = %q", got)
	}
}

func TestSerialConsole_TaggedLine_IsClaimedBeforeSyslogWhenAllBranchesAreOn(t *testing.T) {
	cfg := defaultConfig()
	if !slices.Contains(cfg.Branches, "syslog") || !slices.Contains(cfg.Branches, "serialconsole") {
		t.Fatalf("default branches lack syslog or serialconsole: %v", cfg.Branches)
	}

	lr, res := newHarness(t, cfg).run(serialInput(serialGcfsd))

	notSyslog(t, lr)
	if attrStr(t, lr.Attributes(), "log.record.original") != serialGcfsd || attrStr(t, res.Attributes(), "host.name") != "gk3-cluster-a-nap-w7v5" {
		t.Fatalf("serialconsole did not shape the record: %v %v", lr.Attributes().AsRaw(), res.Attributes().AsRaw())
	}
	w4Body(t, lr, "mounted layer")
}

func TestSerialConsole_LevelAndMsg_SetSeverityAndBody(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialGcfsd))

	notSyslog(t, lr)
	w4Body(t, lr, "mounted layer")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
}

func TestSerialConsole_LevelWithoutMsg_BodyIsTheTextAfterTheTag(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialNoMsg))

	w4Body(t, lr, `time="2026-11-22T10:00:01Z" level=error layer=3 err=timeout`)
	w4Severity(t, lr, plog.SeverityNumberError, "ERROR")
}

func TestSerialConsole_UnknownLevel_LeavesTheSeverityUnset(t *testing.T) {
	lr, _ := runW4(t, serialInput(serialUnknown))

	notSyslog(t, lr)
	w4Body(t, lr, "odd level")
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
}

func TestSerialConsole_LevelWord_UsesTheLogrusTable(t *testing.T) {
	for word, want := range map[string]plog.SeverityNumber{
		"trace": plog.SeverityNumberTrace, "debug": plog.SeverityNumberDebug, "warning": plog.SeverityNumberWarn,
		"fatal": plog.SeverityNumberFatal, "panic": plog.SeverityNumberFatal,
	} {
		lr, _ := runW4(t, serialInput(serialTS+`svc[7]: level=`+word+` msg="x"`+`\r\n`))

		if lr.SeverityNumber() != want {
			t.Errorf("level=%s gave severity %d, want %d", word, lr.SeverityNumber(), want)
		}
	}
}

func TestSerialConsole_ShaLabelRun_IsSummarisedAndTheLineKept(t *testing.T) {
	line, _ := serialSnapshot(6)

	lr, _ := runW4(t, serialInput(line))

	w4Body(t, lr, "snapshot labels: [6 sha256 labels]")
	w4Severity(t, lr, plog.SeverityNumberWarn, "WARN")
	if got := attrStr(t, lr.Attributes(), "log.record.original"); got != line {
		t.Fatalf("log.record.original = %q, want the full line", got)
	}
}

func TestSerialConsole_ShaLabelRun_SurvivesWithoutMsgKey(t *testing.T) {
	_, labels := serialSnapshot(5)

	lr, _ := runW4(t, serialInput(serialTS+`containerd-gcfs-grpc[2997]: labels `+labels+`\r\n`))

	w4Body(t, lr, "labels [5 sha256 labels]")
}

func TestSerialConsole_SingleShaLabel_IsKept(t *testing.T) {
	_, labels := serialSnapshot(1)

	lr, _ := runW4(t, serialInput(serialTS+`svc[7]: level=info msg="one `+labels+`"`+`\r\n`))

	w4Body(t, lr, "one "+labels)
}

func TestSerialConsole_TagWithoutPid_IsShapedAsUntagged(t *testing.T) {
	lr, res := runW4(t, serialInput(serialNoPid))

	w4Body(t, lr, strings.TrimSpace(strings.TrimSuffix(serialNoPid, `\r\n`)))
	w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
	absent(t, lr.Attributes(), "process.pid")
	absent(t, res.Attributes(), "service.name")
}

func TestSerialConsole_LineWithoutTag_IsShapedAsUntagged(t *testing.T) {
	lr, res := runW4(t, serialInput(serialNoTag))

	w4Body(t, lr, strings.TrimSuffix(serialNoTag, `\r\n`))
	absent(t, lr.Attributes(), "process.pid")
	absent(t, res.Attributes(), "service.name")
}

func TestSerialConsole_OtherSource_IsNotClaimed(t *testing.T) {
	in := input{body: serialOK, attrs: map[string]any{"cloud.resource_id": "other"}}

	lr, _ := runW4(t, in)

	if lr.Body().Str() != serialOK || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("record changed: %q", lr.Body().Str())
	}
}
