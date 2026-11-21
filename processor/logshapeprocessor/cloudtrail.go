// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
)

// cloudTrailTargetKeys are the request parameters that name the object of an
// API call, most specific first. An ARN value is cut to its last path part.
var cloudTrailTargetKeys = []string{
	"name", "roleArn", "roleName", "bucketName", "functionName", "instanceId",
	"userName", "tableName", "secretId", "keyId", "clusterName", "logGroupName",
}

// cloudTrailBranch shapes AWS CloudTrail event records (log.source aws.cloudtrail).
type cloudTrailBranch struct {
	aliases map[string]string
}

func newCloudTrailBranch(aliases map[string]string) *cloudTrailBranch {
	return &cloudTrailBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*cloudTrailBranch) name() string { return "cloudtrail" }

func (*cloudTrailBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "aws.cloudtrail" }

func (b *cloudTrailBranch) parse(r *rec) error {
	a := r.lr.Attributes()

	method, source := auditStr(a, "rpc.method"), ""
	if i := strings.IndexByte(method, '/'); i >= 0 {
		method, source = method[i+1:], method[:i]
	} else {
		source = auditStr(a, "rpc.service")
	}
	action := method
	if method != "" && source != "" {
		short, _, _ := strings.Cut(source, ".")
		action = short + ":" + method
	}
	if action != "" {
		auditSetBody(r.lr, body.Audit(action, cloudTrailTarget(a), ""))
	}

	// An ARN gives the role session or user name; an AWS service caller has no ARN.
	who := auditFirstStr(r, "aws.principal.arn", "aws.user_identity.principal.arn")
	if who == "" {
		who = auditFirstStr(r, "user.name", "aws.user_identity.invoked_by", "aws.principal.invoked_by")
	}
	auditService(r.res, principal.Shorten(who, nil))

	auditHost(r.res, auditFirstStr(r, "cloud.account.id"), b.aliases, true)

	// Every error code stays WARN, AccessDenied included.
	if auditStr(a, "aws.error.code") != "" {
		auditSetLevel(r.lr, auditWord("warn"))
	}
	return nil
}

func cloudTrailTarget(a pcommon.Map) string {
	v, ok := a.Get("aws.request.parameters")
	if !ok || v.Type() != pcommon.ValueTypeMap {
		return ""
	}
	params := v.Map()
	for _, k := range cloudTrailTargetKeys {
		if s := auditStr(params, k); s != "" {
			if strings.HasPrefix(s, "arn:") {
				s = s[strings.LastIndexByte(s, '/')+1:]
			}
			return s
		}
	}
	return ""
}
