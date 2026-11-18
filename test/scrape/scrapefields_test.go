// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

func TestScrapeFields_AccessLogsCarryTheServiceName(t *testing.T) {
	got := runChain(t, decoded(newALBDecoder, fixtureFile("testdata/alb-access.log")), tagMaker(t, "aws"))

	if len(got) == 0 {
		t.Fatal("no records")
	}
	res := got[0].Resource
	if res["cloud.service.name"] != "elasticloadbalancing" {
		t.Errorf("cloud.service.name = %v, want elasticloadbalancing", res["cloud.service.name"])
	}
	// This module builds against the upstream decoder, so region and account (read from ARNs by the patched
	// copy of the extension) are asserted in that extension's own tests.
	if res["cloud.provider"] != "aws" {
		t.Errorf("cloud.provider = %v, want aws", res["cloud.provider"])
	}
}

func TestScrapeFields_GCPAuditRecordsCarryServiceAndSeverity(t *testing.T) {
	got := runChain(t, decoded(gcpDecoder, compactFile("testdata/gcp-audit-entry.json")), tagMaker(t, "gcp"))

	if len(got) == 0 {
		t.Fatal("no records")
	}
	if got[0].Resource["cloud.service.name"] != "cloudaudit.googleapis.com" {
		t.Errorf("cloud.service.name = %v, want cloudaudit.googleapis.com", got[0].Resource["cloud.service.name"])
	}
	if got[0].Severity == plog.SeverityNumberUnspecified {
		t.Errorf("severity is unset: %+v", got[0])
	}
}
