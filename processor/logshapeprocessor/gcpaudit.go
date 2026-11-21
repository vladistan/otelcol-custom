// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
)

// grpcCodeNames are the canonical names of the RPC status codes in GCP audit entries.
var grpcCodeNames = [...]string{
	"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
	"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
	"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
	"UNAUTHENTICATED",
}

// gcpAuditBranch shapes Google Cloud audit log entries (log.source gcp.audit).
type gcpAuditBranch struct {
	aliases map[string]string
}

func newGCPAuditBranch(aliases map[string]string) *gcpAuditBranch {
	return &gcpAuditBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*gcpAuditBranch) name() string { return "gcpaudit" }

func (*gcpAuditBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "gcp.audit" }

func (b *gcpAuditBranch) parse(r *rec) error {
	a := r.lr.Attributes()
	// The encoder may still emit the old name for the status code.
	code, hasCode := auditInt(a, "rpc.status_code")
	if !hasCode {
		code, hasCode = auditInt(a, "rpc.response.status_code")
	}
	failed := hasCode && code != 0

	text := r.body
	if text == "" {
		text = body.Audit(auditStr(a, "gcp.audit.method.name"), auditStr(a, "gcp.audit.resource.name"), "")
	}
	status := ""
	if failed {
		status = grpcStatusName(code)
	}
	// A second pass finds the status already in the body.
	if status != "" && (text == status || strings.HasSuffix(text, " "+status)) {
		status = ""
	}
	auditSetBody(r.lr, body.Audit(text, "", status))

	auditService(r.res, principal.Shorten(auditStr(a, "user.email"), nil))

	project := auditFirstStr(r, "gcp.label.project_id", "cloud.account.id")
	k8s := auditStr(a, "gcp.audit.service.name") == "k8s.io" || auditFirstStr(r, "gcp.resource_type") == "k8s_cluster"
	if k8s {
		auditHost(r.res, project, b.aliases, false)
	}
	if alias := b.aliases[project]; project != "" && alias != "" {
		r.res.Attributes().PutStr("cloud.account.name", alias)
	}

	switch {
	case failed:
		auditSetLevel(r.lr, auditWord("error"))
	case k8s && r.lr.SeverityNumber() == plog.SeverityNumberUnspecified:
		auditSetLevel(r.lr, auditWord("info"))
	}
	return nil
}

func grpcStatusName(code int64) string {
	if code >= 0 && code < int64(len(grpcCodeNames)) {
		return grpcCodeNames[code]
	}
	return strconv.FormatInt(code, 10)
}
