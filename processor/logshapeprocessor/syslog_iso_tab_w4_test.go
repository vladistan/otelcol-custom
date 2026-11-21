// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
)

func TestSyslogIso_TabAfterTheTimestamp_IsLeftToTheCollectorBranch(t *testing.T) {
	for _, line := range []string{collectorStandard, collectorEdge} {
		h := newHarness(t, defaultConfig())

		lr, scope := h.runScoped(input{body: line})

		wantBody(t, lr, "Starting HTTP server")
		wantScope(t, scope, "otlp")
		if attrStr(t, lr.Attributes(), "otel.component.name") != "otlp" {
			t.Errorf("%.40q: attributes = %v", line, lr.Attributes().AsRaw())
		}
		absent(t, lr.Attributes(), "process.pid")
	}
}

func TestSyslogIso_TabAfterTheTimestamp_SyslogBranchAloneDoesNotClaim(t *testing.T) {
	for _, line := range []string{collectorStandard, collectorEdge} {
		if shapedBy(t, []string{"syslog"}, input{body: line}) {
			t.Errorf("syslog branch shaped %.40q", line)
		}
	}
}

func TestSyslogIso_SpaceAfterTheTimestamp_IsStillUnwrapped(t *testing.T) {
	lr, res := runW4(t, input{body: wrapPlainIso})

	w4Body(t, lr, "Accepted publickey for tyler")
	if resText(t, res, "host.name") != "nas" || resText(t, res, "service.name") != "sshd" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}
