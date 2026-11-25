// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// lldpdKeys are the fields of a neighbour record that become vstan.lldp.<key>.
var lldpdKeys = []string{"chassis_id", "chassis_name", "chassis_descr", "port_id", "port_descr", "via", "age"}

// lldpdBranch shapes the neighbour records of lldpd, which arrive as a JSON object body with the
// interface, the chassis and the port of the peer. The body becomes one line, observed_at is the
// record time, and the severity stays unset because the record carries no level.
type lldpdBranch struct{}

func newLldpdBranch() *lldpdBranch { return &lldpdBranch{} }

func (*lldpdBranch) name() string { return "lldpd" }

func (*lldpdBranch) claims(r *rec) bool {
	return strings.HasPrefix(r.body, "{") && strings.Contains(r.body, `"chassis_id"`) &&
		strings.Contains(r.body, `"port_id"`) && strings.Contains(r.body, `"interface"`)
}

func (*lldpdBranch) parse(r *rec) error {
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		return err
	}
	iface, chassis, port := textOf(m["interface"]), textOf(m["chassis_name"]), textOf(m["port_id"])
	if iface == "" || chassis == "" || port == "" {
		return errors.New("lldpd record misses the interface, chassis name or port")
	}
	var observed time.Time
	if raw := textOf(m["observed_at"]); raw != "" {
		var err error
		if observed, err = time.Parse(time.RFC3339Nano, raw); err != nil {
			return err
		}
	}

	a := r.lr.Attributes()
	// The file receiver copied the JSON fields to attributes under their own names; they move to official names.
	for _, k := range []string{"interface", "client.mac", "client.ip", "mgmt_ip", "observed_at"} {
		a.Remove(k)
	}
	for _, k := range lldpdKeys {
		a.Remove(k)
		if v := textOf(m[k]); v != "" {
			a.PutStr("vstan.lldp."+k, v)
		}
	}
	a.PutStr("log.record.original", r.body)
	a.PutStr("network.interface.name", iface)
	if mac := textOf(m["chassis_id"]); mac != "" {
		a.PutStr("client.mac.address", mac)
	}
	text := iface + " sees " + chassis + " port " + port
	if ips, ok := m["mgmt_ip"].([]any); ok && len(ips) > 0 {
		list := a.PutEmptySlice("vstan.lldp.mgmt_ip")
		for _, ip := range ips {
			list.AppendEmpty().SetStr(textOf(ip))
		}
		if first := textOf(ips[0]); first != "" {
			a.PutStr("client.address", first)
			text += " " + first
		}
	}
	if !observed.IsZero() {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(observed.UTC()))
	}
	r.lr.Body().SetStr(text)
	return nil
}

// textOf returns the string value of a JSON field, or "" for any other type.
func textOf(v any) string {
	s, _ := v.(string)
	return s
}
