// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"

	"github.com/vladistan/otelcol-custom/shape/httpaccess"
)

// oauth2ProxyUser captures the user column, which the shared parser only accepts as "-".
var oauth2ProxyUser = regexp.MustCompile(`^(\S+ - \S+ - )(\S+)( \[.*)$`)

// oauth2ProxyBranch shapes oauth2-proxy request lines; its auth and startup lines stay unmatched.
type oauth2ProxyBranch struct{}

func newOAuth2ProxyBranch() *oauth2ProxyBranch { return &oauth2ProxyBranch{} }

func (*oauth2ProxyBranch) name() string { return "oauth2proxy" }

func (*oauth2ProxyBranch) claims(r *rec) bool {
	_, _, ok := parseOAuth2Proxy(r.body)
	return ok
}

func (*oauth2ProxyBranch) parse(r *rec) error {
	e, user, ok := parseOAuth2Proxy(r.body)
	if !ok {
		return errors.New("not an oauth2-proxy access line")
	}
	e.User = user
	a := r.lr.Attributes()
	putAccess(a, e)
	if e.RequestID != "" {
		a.PutStr("vstan.oauth2proxy.request_id", e.RequestID)
	}
	setHTTPSeverity(r.lr, e.Status)
	// An earlier stage may already hold the raw line; keep the first one.
	if _, ok := a.Get("log.record.original"); !ok {
		a.PutStr("log.record.original", r.body)
	}
	r.lr.Body().SetStr(e.Body())
	return nil
}

// parseOAuth2Proxy returns the entry and the user ("" for "-").
func parseOAuth2Proxy(line string) (httpaccess.Entry, string, bool) {
	user := ""
	if m := oauth2ProxyUser.FindStringSubmatch(line); m != nil && m[2] != "-" {
		user = m[2]
		line = m[1] + "-" + m[3]
	}
	e, ok := httpaccess.ParseOAuth2Proxy(line)
	return e, user, ok
}
