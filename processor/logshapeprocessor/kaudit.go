// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
)

var (
	auditShape = regexp.MustCompile(`^audit: type=(\d+) audit\((\d+)\.(\d+):(\d+)\): (.*)$`)
	auditPair  = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)
)

// kernelAuditBranch shapes kernel audit lines that carry an AppArmor decision.
type kernelAuditBranch struct{}

func newKernelAuditBranch() *kernelAuditBranch { return &kernelAuditBranch{} }

func (*kernelAuditBranch) name() string { return "kaudit" }

// auditLine is the line without the spaces and line ends around it.
func auditLine(body string) string { return strings.Trim(body, " \r\n") }

func (*kernelAuditBranch) claims(r *rec) bool {
	m := auditShape.FindStringSubmatch(auditLine(r.body))
	return m != nil && strings.Contains(m[5], "apparmor=")
}

func (*kernelAuditBranch) parse(r *rec) error {
	m := auditShape.FindStringSubmatch(auditLine(r.body))
	if m == nil {
		return errors.New("not an audit line")
	}
	sec, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return err
	}
	frac := (m[3] + "000000000")[:9]
	nsec, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return err
	}
	kv := map[string]string{}
	for _, p := range auditPair.FindAllStringSubmatch(m[5], -1) {
		v := p[2]
		if v == "" {
			v = p[3]
		}
		kv[p[1]] = v
	}
	text := strings.Join(nonEmpty(strings.ToUpper(kv["apparmor"]), kv["operation"], firstOf(kv["requested"], kv["requested_mask"])), " ")
	if kv["profile"] != "" {
		text += ", profile " + kv["profile"]
	}
	if addr := firstOf(kv["faddr"], kv["addr"]); addr != "" {
		text += ", to " + addr
		if port := firstOf(kv["fport"], kv["port"]); port != "" {
			text += ":" + port
		}
	}

	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	typ, _ := strconv.ParseInt(m[1], 10, 64)
	serial, _ := strconv.ParseInt(m[4], 10, 64)
	a.PutInt("vstan.audit.type", typ)
	a.PutInt("vstan.audit.serial", serial)
	r.res.Attributes().PutStr("service.name", "audit")
	if _, ok := a.Get("log.source"); !ok {
		a.PutStr("log.source", "kernel")
	}
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(time.Unix(sec, nsec).UTC()))
	applyLevel(r.lr, wordLevel("info"))
	r.lr.Body().SetStr(body.Compose("apparmor", text))
	return nil
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
