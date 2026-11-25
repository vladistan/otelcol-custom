// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"math"
	"net"
	"regexp"
	"strconv"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// coreDNSQuery reads the log plugin line: client, id, "type class name proto size do bufsize", rcode, flags, size, seconds.
var coreDNSQuery = regexp.MustCompile(`^(?:\[[A-Z]+\] )?(\S+) - \d+ "(\S+) (\S+) (\S+) (\S+) \d+ \S+ \d+" (\S+) \S+ \d+ ([0-9.]+)s$`)

// coreDNSBranch shapes CoreDNS query log lines.
type coreDNSBranch struct{}

func newCoreDNSBranch() *coreDNSBranch { return &coreDNSBranch{} }

func (*coreDNSBranch) name() string { return "coredns" }

func (*coreDNSBranch) claims(r *rec) bool { return coreDNSQuery.MatchString(r.body) }

func (*coreDNSBranch) parse(r *rec) error {
	m := coreDNSQuery.FindStringSubmatch(r.body)
	if m == nil {
		return errors.New("not a coredns query line")
	}
	secs, err := strconv.ParseFloat(m[7], 64)
	if err != nil {
		return err
	}
	qtype, qname, rcode := m[2], m[4], m[6]
	a := r.lr.Attributes()
	if host, port, err := net.SplitHostPort(m[1]); err == nil {
		a.PutStr("client.address", host)
		if p, err := strconv.ParseInt(port, 10, 64); err == nil {
			a.PutInt("client.port", p)
		}
	} else {
		a.PutStr("client.address", m[1])
	}
	a.PutStr("dns.question.name", qname)
	a.PutStr("vstan.dns.question.type", qtype)
	a.PutStr("vstan.dns.response_code", rcode)
	a.PutDouble("vstan.dns.duration", secs)
	if l, ok := severity.FromDNSResponse(rcode); ok {
		r.lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
		r.lr.SetSeverityText(l.Text)
	}
	// An earlier stage may already hold the raw line; keep the first one.
	if _, ok := a.Get("log.record.original"); !ok {
		a.PutStr("log.record.original", r.body)
	}
	r.lr.Body().SetStr(rcode + " " + qtype + " " + qname + " " + body.FormatDuration(int64(math.Round(secs*1e6))))
	return nil
}
