// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/principal"
)

// s3AccessBranch shapes S3 server access records, which arrive with an empty body.
type s3AccessBranch struct {
	aliases map[string]string
}

func newS3AccessBranch(aliases map[string]string) *s3AccessBranch {
	return &s3AccessBranch{aliases: principal.MergeAliases(nil, aliases)}
}

func (*s3AccessBranch) name() string { return "s3access" }

func (*s3AccessBranch) claims(r *rec) bool { return sourceOf(r.lr, r.res) == "s3access" }

func (b *s3AccessBranch) parse(r *rec) error {
	a, res := r.lr.Attributes(), r.res.Attributes()
	bucket := lbString("aws.s3.bucket", res, a)
	key := strings.TrimPrefix(lbString("aws.s3.key", a), "/")
	if key == "" {
		key = s3accessKeyFromPath(lbString("url.path", a), bucket)
	}
	path := ""
	if key != "" {
		path = "/" + key
	}
	status, hasStatus := lbInt(a, "http.response.status_code")
	size, _ := lbInt(a, "http.response.body.size")
	lbSetBody(r, body.HTTP(body.HTTPLine{
		Method:    lbString("rpc.method", a),
		Host:      bucket,
		Path:      path,
		Status:    status,
		SizeBytes: int64(size),
	}))
	lbSetSeverity(r.lr, status, hasStatus)

	if bucket != "" {
		res.PutStr("host.name", bucket)
	}
	if user := lbString("user.id", a); user != "-" {
		if svc := principal.Shorten(user, nil); svc != "" {
			res.PutStr("service.name", svc)
		}
	}
	// The encoder emits no cloud.account.id: the scrape config adds it as a resource
	// attribute on the S3 access source (one value per source or bucket), else no alias applies.
	lbSetAccountName(res, a, b.aliases)
	if v, ok := a.Get("source.address"); ok {
		if _, exists := a.Get("client.address"); !exists {
			v.CopyTo(a.PutEmpty("client.address"))
		}
		a.Remove("source.address")
	}
	return nil
}

// s3accessKeyFromPath returns the object key of a request path, which may carry the bucket as its first segment.
func s3accessKeyFromPath(p, bucket string) string {
	p = strings.TrimPrefix(p, "/")
	if bucket != "" && (p == bucket || strings.HasPrefix(p, bucket+"/")) {
		p = strings.TrimPrefix(strings.TrimPrefix(p, bucket), "/")
	}
	return p
}
