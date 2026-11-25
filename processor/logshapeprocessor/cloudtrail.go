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

const (
	cloudTrailRedacted = "[REDACTED]"
	cloudTrailFallback = "cloudtrail"
	cloudTrailKeyMax   = 40
	cloudTrailKeyHead  = 22
	cloudTrailKeyTail  = 16

	// cloudTrailMachineDigits is the length from which an all-digit session name counts as machine-generated.
	cloudTrailMachineDigits = 10
	cloudTrailSessionAttr   = "aws.user_identity.session_name"
)

// cloudTrailBranch shapes AWS CloudTrail event records (log.source cloudtrail).
type cloudTrailBranch struct {
	aliases     map[string]string
	userAliases map[string]string
}

func newCloudTrailBranch(aliases, userAliases map[string]string) *cloudTrailBranch {
	return &cloudTrailBranch{aliases: principal.MergeAliases(nil, aliases), userAliases: userAliases}
}

func (*cloudTrailBranch) name() string { return "cloudtrail" }

func (*cloudTrailBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "cloudtrail" }

func (b *cloudTrailBranch) parse(r *rec) error {
	a := r.lr.Attributes()

	method, source := auditStr(a, "rpc.method"), ""
	if i := strings.IndexByte(method, '/'); i >= 0 {
		method, source = method[i+1:], method[:i]
	} else {
		source = auditStr(a, "rpc.service")
	}
	action, short := method, ""
	if method != "" && source != "" {
		short, _, _ = strings.Cut(source, ".")
		action = short + ":" + method
	}
	if action != "" {
		line := cloudTrailBody(a, action, short, method)
		if short == "sso" && method == "GetRoleCredentials" && cloudTrailIdentityType(r) == cloudTrailIdentityCenter {
			line = b.roleCredentialsBody(r, action)
		}
		auditSetBody(r.lr, line)
	}

	auditService(r.res, b.caller(r))
	if _, session := cloudTrailAssumedRole(r); cloudTrailMachineSession(session) {
		a.PutStr(cloudTrailSessionAttr, session)
	}
	auditHost(r.res, auditFirstStr(r, "cloud.account.id"), b.aliases, true)

	if code := auditStr(a, "aws.error.code"); code == "AccessDenied" {
		auditSetLevel(r.lr, auditWord("error"))
	} else if code != "" {
		auditSetLevel(r.lr, auditWord("warn"))
	}

	if short == "sts" && method == "AssumeRole" {
		cloudTrailRedact(a)
	}
	return nil
}

// caller names the service from the identity type; a missing or unknown type gives "cloudtrail".
func (b *cloudTrailBranch) caller(r *rec) string {
	who := ""
	switch cloudTrailIdentityType(r) {
	case cloudTrailIdentityCenter:
		return b.identityCenterCaller(r)
	case "AWSService":
		who = cloudTrailInvoker(auditFirstStr(r, "aws.user_identity.invoked_by", "aws.principal.invoked_by"))
	case "AssumedRole":
		who = cloudTrailRoleSession(r)
	case "AWSAccount":
		who = principal.AccountAlias(auditFirstStr(r, "aws.user_identity.account_id", "cloud.account.id"), b.aliases)
	case "SAMLUser", "WebIdentityUser", "IAMUser":
		who = auditFirstStr(r, "user.name")
	}
	if who == "" {
		return cloudTrailFallback
	}
	return principal.Shorten(who, nil)
}

const cloudTrailIdentityCenter = "IdentityCenterUser"

func cloudTrailIdentityType(r *rec) string {
	return auditFirstStr(r, "aws.user_identity.type", "aws.principal.type", "aws.user_identity.principal.type")
}

// identityCenterCaller is the alias of the user GUID, else the whole GUID; Shorten would cut a GUID, so it is
// not applied. A row with no GUID gives "cloudtrail".
func (b *cloudTrailBranch) identityCenterCaller(r *rec) string {
	guid := auditFirstStr(r, "user.id")
	if name := b.userAliases[guid]; name != "" {
		return name
	}
	if guid == "" {
		return cloudTrailFallback
	}
	return guid
}

// roleCredentialsBody builds "sso:GetRoleCredentials <role>@<account> from <address>". The account goes through
// account_aliases; a missing role or account drops the "@" part and a missing address drops the "from" part.
func (b *cloudTrailBranch) roleCredentialsBody(r *rec, action string) string {
	target := ""
	role, account := auditFirstStr(r, "aws.service_event.role_name"), auditFirstStr(r, "aws.service_event.account_id")
	if role != "" && account != "" {
		target = role + "@" + principal.AccountAlias(account, b.aliases)
	}
	line := body.Audit(action, target, "")
	if address := auditFirstStr(r, "source.address"); address != "" {
		line += " from " + address
	}
	return line
}

// cloudTrailInvoker gives the service label of an invoking AWS service host name.
func cloudTrailInvoker(by string) string {
	if before, ok := strings.CutSuffix(by, ".amazonaws.com"); ok {
		return before
	}
	return by
}

// cloudTrailMachineSession reports whether a session name was generated by a program: all digits, ten or more
// (a timestamp). Such a name differs on every call, so it names neither the caller nor the target.
func cloudTrailMachineSession(session string) bool {
	if len(session) < cloudTrailMachineDigits {
		return false
	}
	for _, c := range session {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cloudTrailAssumedRole gives the role and the session of an assumed role caller: the role is the issuer name
// when the record has one, else the role of the principal ARN.
func cloudTrailAssumedRole(r *rec) (role, session string) {
	arn := auditFirstStr(r, "aws.principal.arn", "aws.user_identity.principal.arn")
	if i := strings.Index(arn, "assumed-role/"); i >= 0 {
		role, session, _ = strings.Cut(arn[i+len("assumed-role/"):], "/")
	}
	if issuer := auditFirstStr(r, "aws.user_identity.session_context.issuer.user_name"); issuer != "" {
		role = issuer
	}
	return role, session
}

// cloudTrailRoleSession joins the role and the session name with "/"; a machine-generated session is left out.
func cloudTrailRoleSession(r *rec) string {
	role, session := cloudTrailAssumedRole(r)
	if cloudTrailMachineSession(session) {
		return role
	}
	switch {
	case role == "":
		return session
	case session == "":
		return role
	}
	return role + "/" + session
}

// cloudTrailBody builds the action line: AssumeRole names role and session, S3 names bucket and key.
func cloudTrailBody(a pcommon.Map, action, source, method string) string {
	params := cloudTrailParams(a)
	switch {
	case source == "sts" && method == "AssumeRole":
		return strings.TrimSpace(body.Audit(action, cloudTrailTarget(a), "") + " " + cloudTrailSession(a, params))
	case source == "s3":
		bucket := auditStr(params, "bucketName")
		if key := auditStr(params, "key"); bucket != "" && key != "" {
			return body.Audit(action, bucket+"/"+cloudTrailCutKey(key), "")
		}
	}
	return body.Audit(action, cloudTrailTarget(a), "")
}

// cloudTrailSession gives the session name for the AssumeRole body; a machine-generated one is left out.
func cloudTrailSession(a, params pcommon.Map) string {
	session := cloudTrailTargetSession(a, params)
	if cloudTrailMachineSession(session) {
		return ""
	}
	return session
}

// cloudTrailTargetSession gives the requested session name, else the session of the assumed role user in the response.
func cloudTrailTargetSession(a, params pcommon.Map) string {
	if s := auditStr(params, "roleSessionName"); s != "" {
		return s
	}
	v, ok := a.Get("aws.response.elements")
	if !ok || v.Type() != pcommon.ValueTypeMap {
		return ""
	}
	u, ok := v.Map().Get("assumedRoleUser")
	if !ok || u.Type() != pcommon.ValueTypeMap {
		return ""
	}
	arn := auditStr(u.Map(), "arn")
	return arn[strings.LastIndexByte(arn, '/')+1:]
}

// cloudTrailCutKey cuts the middle out of a long object key.
func cloudTrailCutKey(key string) string {
	if len(key) <= cloudTrailKeyMax {
		return key
	}
	return key[:cloudTrailKeyHead] + ".." + key[len(key)-cloudTrailKeyTail:]
}

// cloudTrailRedact replaces the key id and session token of an AssumeRole credentials block.
func cloudTrailRedact(a pcommon.Map) {
	v, ok := a.Get("aws.response.elements")
	if !ok || v.Type() != pcommon.ValueTypeMap {
		return
	}
	c, ok := v.Map().Get("credentials")
	if !ok || c.Type() != pcommon.ValueTypeMap {
		return
	}
	for _, k := range []string{"accessKeyId", "sessionToken"} {
		if _, ok := c.Map().Get(k); ok {
			c.Map().PutStr(k, cloudTrailRedacted)
		}
	}
}

func cloudTrailParams(a pcommon.Map) pcommon.Map {
	if v, ok := a.Get("aws.request.parameters"); ok && v.Type() == pcommon.ValueTypeMap {
		return v.Map()
	}
	return pcommon.NewMap()
}

func cloudTrailTarget(a pcommon.Map) string {
	params := cloudTrailParams(a)
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
