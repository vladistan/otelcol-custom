// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
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

// gcpAuditBranch shapes Google Cloud audit log entries (log.source audit).
type gcpAuditBranch struct {
	aliases map[string]string
}

func newGCPAuditBranch(aliases map[string]string) *gcpAuditBranch {
	return &gcpAuditBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*gcpAuditBranch) name() string { return "gcpaudit" }

func (*gcpAuditBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "audit" }

func (b *gcpAuditBranch) parse(r *rec) error {
	a := r.lr.Attributes()
	// Some encoders emit rpc.response.status_code instead of rpc.status_code.
	code, hasCode := auditInt(a, "rpc.status_code")
	if !hasCode {
		code, hasCode = auditInt(a, "rpc.response.status_code")
	}
	failed := hasCode && code != 0

	status := ""
	if failed {
		status = grpcStatusName(code)
	}
	auditSetBody(r.lr, gcpAuditBody(r.body, a, status))

	who := auditStr(a, "user.email")
	if who == "" {
		who = auditStr(a, "user.id")
	}
	auditService(r.res, principal.Shorten(who, nil))

	project := auditFirstStr(r, "gcp.label.project_id", "cloud.account.id")
	k8s := strings.HasPrefix(auditStr(a, "gcp.audit.method.name"), "io.k8s.") ||
		auditStr(a, "gcp.audit.service.name") == "k8s.io" || auditFirstStr(r, "gcp.resource_type") == "k8s_cluster"
	if cluster := auditFirstStr(r, "gcp.label.cluster_name"); k8s && cluster != "" {
		r.res.Attributes().PutStr("host.name", cluster)
	} else {
		auditHost(r.res, project, b.aliases, false)
	}
	setAccountName(r.res.Attributes(), project, b.aliases)

	switch {
	case failed:
		auditSetLevel(r.lr, auditWord("error"))
	case k8s && r.lr.SeverityNumber() == plog.SeverityNumberUnspecified:
		auditSetLevel(r.lr, auditWord("info"))
	}
	return nil
}

// gcpAuditBody returns "method resource status". An empty line is built from
// the method and resource attributes. A status already ending the line is not repeated.
func gcpAuditBody(line string, a pcommon.Map, status string) string {
	if status != "" && (line == status || strings.HasSuffix(line, " "+status)) {
		line = strings.TrimSpace(strings.TrimSuffix(line, status))
	}
	method, resource, _ := strings.Cut(line, " ")
	if line == "" {
		method, resource = auditStr(a, "gcp.audit.method.name"), auditStr(a, "gcp.audit.resource.name")
	}
	return body.Audit(methodPair(method), shortResource(resource), status)
}

// methodPair keeps the last two dot-separated elements of a method name.
func methodPair(method string) string {
	parts := strings.Split(method, ".")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}

// shortResource turns a path with a "namespaces" or "zones" segment into
// "scope/name"; any other resource is returned as it is.
func shortResource(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if (p == "namespaces" || p == "zones") && len(parts) > i+3 {
			return parts[i+1] + "/" + parts[i+3]
		}
	}
	return path
}

func grpcStatusName(code int64) string {
	if code >= 0 && code < int64(len(grpcCodeNames)) {
		return grpcCodeNames[code]
	}
	return strconv.FormatInt(code, 10)
}
