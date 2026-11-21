// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore vstan

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// eksAuditBranch shapes the JSON audit events the API server of an EKS cluster
// writes to CloudWatch (log.source aws.eks).
type eksAuditBranch struct {
	aliases map[string]string
	// last holds the event decoded by claims so parse of the same record
	// does not decode it again; a miss only costs one more decode.
	last atomic.Pointer[eksDecoded]
}

type eksDecoded struct {
	text string
	ev   map[string]any
}

// eksMaxBody is the largest body decoded; real audit events are a few KiB.
const eksMaxBody = 256 << 10

func newEKSAuditBranch(aliases map[string]string) *eksAuditBranch {
	return &eksAuditBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*eksAuditBranch) name() string { return "eksaudit" }

// claims takes an aws.eks record whose body is an audit event. Other aws.eks
// lines are application logs.
func (b *eksAuditBranch) claims(r *rec) bool {
	if sourceOf(r.lr, r.res) != "aws.eks" {
		return false
	}
	// Cheap byte checks first: most aws.eks lines are not audit events.
	if len(r.body) > eksMaxBody || (!strings.Contains(r.body, `"verb"`) && !strings.Contains(r.body, `"auditID"`)) {
		return false
	}
	ev, ok := eksEvent(r.body)
	if !ok || (eksStr(ev, "verb") == "" && eksStr(ev, "auditID") == "") {
		return false
	}
	b.last.Store(&eksDecoded{text: r.body, ev: ev})
	return true
}

func (b *eksAuditBranch) decode(text string) (map[string]any, bool) {
	if d := b.last.Load(); d != nil && d.text == text {
		return d.ev, true
	}
	return eksEvent(text)
}

func eksEvent(text string) (map[string]any, bool) {
	if len(text) > eksMaxBody {
		return nil, false
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return nil, false
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(text), &ev); err != nil || ev == nil {
		return nil, false
	}
	return ev, true
}

func (b *eksAuditBranch) parse(r *rec) error {
	ev, ok := b.decode(r.body)
	if !ok {
		return errors.New("not an audit event")
	}
	a := r.lr.Attributes()
	verb := body.Verb(eksStr(ev, "verb"))

	status := ""
	code, hasCode := auditNumber(eksAt(ev, "responseStatus", "code"))
	if hasCode {
		status = strconv.FormatInt(code, 10)
	}

	if _, has := a.Get("log.record.original"); !has {
		a.PutStr("log.record.original", r.body)
	}
	if verb != "" {
		a.PutStr("http.request.method", verb)
	}
	// "level" is the audit detail level (Metadata, Request, RequestResponse), never a severity.
	if level := eksStr(ev, "level"); level != "" {
		a.PutStr("vstan.audit.level", level)
	}
	if hasCode {
		a.PutInt("http.response.status_code", code)
		if l, ok := severity.FromHTTPStatus(int(code)); ok {
			auditSetLevel(r.lr, l)
		}
	}
	auditSetBody(r.lr, body.Audit(verb, eksTarget(ev), status))

	auditService(r.res, principal.Shorten(eksStr(ev, "user", "username"), nil))
	if cluster := eksCluster(auditFirstStr(r, "cloudwatch.log.group.name")); cluster != "" {
		auditHost(r.res, cluster, b.aliases, false)
	}
	return nil
}

// eksTarget builds "resource[/subresource] [namespace/]name" and falls back to the request URI.
func eksTarget(ev map[string]any) string {
	resource := eksStr(ev, "objectRef", "resource")
	if resource == "" {
		return eksStr(ev, "requestURI")
	}
	if sub := eksStr(ev, "objectRef", "subresource"); sub != "" {
		resource += "/" + sub
	}
	ns, name := eksStr(ev, "objectRef", "namespace"), eksStr(ev, "objectRef", "name")
	obj := name
	if ns != "" && name != "" {
		obj = ns + "/" + name
	} else if ns != "" {
		obj = ns
	}
	return body.Audit(resource, obj, "")
}

// eksCluster reads the cluster name of a log group such as /aws/eks/<cluster>/cluster.
func eksCluster(group string) string {
	rest, ok := strings.CutPrefix(group, "/aws/eks/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

func eksAt(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

func eksStr(m map[string]any, path ...string) string {
	s, _ := eksAt(m, path...).(string)
	return s
}
