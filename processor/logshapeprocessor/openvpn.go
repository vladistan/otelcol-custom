// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
)

// openvpnBranch gives the one-line body to the records of the openvpnstatus receiver, which sends
// an empty body, and moves its attributes to the official names (vstan.openvpn.* for the rest).
type openvpnBranch struct{}

func newOpenVPNBranch() *openvpnBranch { return &openvpnBranch{} }

func (*openvpnBranch) name() string { return "openvpn" }

func (*openvpnBranch) claims(r *rec) bool {
	if r.body != "" {
		return false
	}
	a := r.lr.Attributes()
	return (hasKey(a, "common_name") || hasKey(a, "user.name")) &&
		(hasKey(a, "vstan.openvpn.bytes_received") || hasKey(a, "bytes_received")) &&
		(hasKey(a, "vstan.openvpn.bytes_sent") || hasKey(a, "bytes_sent"))
}

func (*openvpnBranch) parse(r *rec) error {
	a := r.lr.Attributes()
	user := firstOf(attrText(a, "common_name"), attrText(a, "user.name"))
	addr := firstOf(attrText(a, "real_address"), attrText(a, "client.address"))
	port := int64(-1)
	if host, p, err := net.SplitHostPort(addr); err == nil {
		addr = host
		if n, err := strconv.ParseInt(p, 10, 64); err == nil {
			port = n
		}
	}
	recv, okIn := firstNumber(a, "vstan.openvpn.bytes_received", "bytes_received")
	sent, okOut := firstNumber(a, "vstan.openvpn.bytes_sent", "bytes_sent")
	if user == "" || !okIn || !okOut {
		return errors.New("openvpn record misses the user or the counters")
	}
	since := firstOf(attrText(a, "vstan.openvpn.connected_since"), attrText(a, "connected_since"))
	text := user + " connected since " + since
	if addr != "" {
		text += " from " + addr
	}
	text += ", in " + body.FormatBytes(recv) + ", out " + body.FormatBytes(sent)

	// Only the old receiver spellings move; every other key of the record stays as it is.
	a.Remove("common_name")
	a.Remove("real_address")
	a.PutStr("user.name", user)
	if addr != "" {
		a.PutStr("client.address", addr)
	}
	if port >= 0 {
		a.PutInt("client.port", port)
	}
	for _, k := range []string{"bytes_received", "bytes_sent", "connected_since"} {
		if v, ok := a.Get(k); ok {
			tmp := pcommon.NewValueEmpty()
			v.CopyTo(tmp)
			tmp.CopyTo(a.PutEmpty("vstan.openvpn." + k))
			a.Remove(k)
		}
	}
	r.lr.Body().SetStr(strings.TrimSpace(text))
	return nil
}

func firstNumber(m pcommon.Map, keys ...string) (int64, bool) {
	for _, k := range keys {
		if n, ok := attrNumber(m, k); ok {
			return n, true
		}
	}
	return 0, false
}

func hasKey(m pcommon.Map, key string) bool { _, ok := m.Get(key); return ok }

func attrText(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

func attrNumber(m pcommon.Map, key string) (int64, bool) {
	v, ok := m.Get(key)
	if !ok {
		return 0, false
	}
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return v.Int(), true
	case pcommon.ValueTypeDouble:
		return int64(v.Double()), true
	}
	return 0, false
}
