// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"math"
	"slices"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// gcplbDurationNames are the attributes that hold the request duration in seconds, in the order they are read.
var gcplbDurationNames = []string{"http.request.server.duration", "http.server.duration", "server.duration"}

// gcpLBBranch shapes Google Cloud load balancer request records. It keys only on
// log.source: the scrape config sets that for every load balancer path.
type gcpLBBranch struct {
	errorValues []string
	aliases     map[string]string
}

func newGCPLBBranch(extra []string, aliases map[string]string) *gcpLBBranch {
	return &gcpLBBranch{errorValues: gcpLBErrorValues(extra), aliases: principal.MergeAliases(nil, aliases)}
}

func (*gcpLBBranch) name() string { return "gcplb" }

func (*gcpLBBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "lb" }

func (b *gcpLBBranch) parse(r *rec) error {
	a, res := r.lr.Attributes(), r.res.Attributes()
	status, hasStatus := lbInt(a, "http.response.status_code")
	line := body.HTTPLine{
		Method: lbString("http.request.method", a),
		Host:   lbString("url.domain", a),
		Path:   lbString("url.path", a),
		Status: status,
	}
	for _, name := range gcplbDurationNames {
		if _, set := a.Get(name); !set {
			continue
		}
		if secs, ok := lbNumber(a, name); ok && secs >= 0 {
			line.DurationMicros, line.HasDuration = int64(math.Round(secs*1e6)), true
		}
		break // the first name that is set is the one used, even when it is not a number
	}
	lbSetBody(r, body.HTTP(line))
	lbSetSeverity(r.lr, status, hasStatus)
	if slices.Contains(b.errorValues, lbString("gcp.load_balancing.status.details", a)) &&
		r.lr.SeverityNumber() < plog.SeverityNumberWarn {
		if warn, ok := severity.FromSource(severity.Word, "WARN"); ok {
			lbApplyLevel(r.lr, warn)
		}
	}

	project := lbString("gcp.label.project_id", res, a)
	if project == "" {
		project = lbString("cloud.account.id", res, a)
	}
	if project != "" {
		res.PutStr("host.name", principal.AccountAlias(project, b.aliases))
		setAccountName(res, project, b.aliases)
	}
	if svc := principal.BackendService(lbString("gcp.label.backend_service_name", res, a)); svc != "" {
		res.PutStr("service.name", svc)
	}
	return nil
}
