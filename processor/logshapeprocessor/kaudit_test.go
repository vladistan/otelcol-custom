// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// INVENTED values in the shape of a kernel audit line of an AppArmor decision on a network call.
// Replace it with a real line when one is delivered.
const auditAllowed = `audit: type=1400 audit(1795129202.123:456): apparmor="ALLOWED" operation="file_perm" class="net" profile="snap.amazon-ssm-agent.amazon-ssm-agent" pid=1234 comm="agent" requested="send" faddr=203.0.113.10 fport=443`

func TestKernelAudit_ApparmorLine_GivesTheOneLineBodyAndTheServiceAudit(t *testing.T) {
	lr, res := runW4(t, input{body: auditAllowed})

	w4Body(t, lr, "[apparmor] ALLOWED file_perm send, profile snap.amazon-ssm-agent.amazon-ssm-agent, to 203.0.113.10:443")
	w4Severity(t, lr, plog.SeverityNumberInfo, "INFO")
	w4Time(t, lr, time.Unix(1795129202, 123000000).UTC())
	a := lr.Attributes()
	attrInt(t, a, "vstan.audit.type", 1400)
	if attrStr(t, a, "log.record.original") != auditAllowed || attrStr(t, res.Attributes(), "service.name") != "audit" {
		t.Fatalf("attributes = %v resource = %v", a.AsRaw(), res.Attributes().AsRaw())
	}
	if attrStr(t, a, "log.source") != "kernel" {
		t.Fatalf("log.source must be kernel when the record has none: %v", a.AsRaw())
	}
}

func TestKernelAudit_RecordWithASource_KeepsIt(t *testing.T) {
	lr, _ := runW4(t, input{body: auditAllowed, attrs: map[string]any{"log.source": "journal"}})

	if attrStr(t, lr.Attributes(), "log.source") != "journal" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}

func TestKernelAudit_LineWithoutAPeer_OmitsTheDestination(t *testing.T) {
	lr, _ := runW4(t, input{body: `audit: type=1400 audit(1795129202.123:457): apparmor="DENIED" operation="open" profile="docker-default" requested="r"`})

	w4Body(t, lr, "[apparmor] DENIED open r, profile docker-default")
}

func TestKernelAudit_AuditLineWithoutApparmor_IsNotClaimed(t *testing.T) {
	const line = `audit: type=1006 audit(1795129202.123:458): pid=1 uid=0 old-auid=4294967295`

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}
