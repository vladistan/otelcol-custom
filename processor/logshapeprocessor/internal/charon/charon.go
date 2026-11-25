// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package charon reads the lines of the strongSwan charon daemon.
package charon

import (
	"regexp"
	"strconv"
	"strings"
)

// Info is one charon line: `07[IKE] <conn|2> message`.
type Info struct {
	Thread    string // the thread number as written, "07"
	Component string // the subsystem: IKE, NET, CFG, CHD, ENC, JOB, KNL, MGR ...
	ConnID    string // the connection name or id, empty when the line has no connection part
	ConnSeq   string // the connection sequence number after the bar
	Message   string // the text after the connection part; a packet or proposal line is shortened
	Extra     map[string]string
}

var (
	linePattern     = regexp.MustCompile(`^(\w+)\[([A-Z]{2,4})\]\s+(?:<([^|>]*)(?:\|(\d*))?>\s+)?(\S.*)$`)
	packetPattern   = regexp.MustCompile(`(received|sending) packet: from ([0-9A-Fa-f:.]+)\[(\d+)\] to ([0-9A-Fa-f:.]+)\[(\d+)\] \((\d+) bytes\)`)
	childSAPattern  = regexp.MustCompile(`CHILD_SA\s+([A-Za-z0-9_-]+)\{(\d+)\}\s+(\w+)`)
	spiPattern      = regexp.MustCompile(`SPIs?\s+([a-f0-9]+)_i\s+([a-f0-9]+)_o`)
	tsPattern       = regexp.MustCompile(`TS\s+([\d./\s=]+)$`)
	proposalPattern = regexp.MustCompile(`selected proposal:\s+(.+)$`)
	deletePattern   = regexp.MustCompile(`DELETE for (\w+) CHILD_SA with SPI ([a-f0-9]+)`)
	authPattern     = regexp.MustCompile(`authentication of '([^']+)' with (.+?) (successful|failed)\b`)
	ikeSAPattern    = regexp.MustCompile(`IKE_SA\s+(\S+)\[(\d+)\]\s+(\w+)`)
)

// Parse reads a charon line. It reports false for any other text.
func Parse(body string) (*Info, bool) {
	m := linePattern.FindStringSubmatch(body)
	if m == nil {
		return nil, false
	}
	info := &Info{Thread: m[1], Component: m[2], ConnID: m[3], ConnSeq: m[4], Message: strings.TrimSpace(m[5]), Extra: map[string]string{}}
	msg := info.Message

	if p := packetPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.direction"] = p[1]
		local, remote := [2]string{p[2], p[3]}, [2]string{p[4], p[5]}
		if p[1] == "received" {
			local, remote = remote, local
		}
		info.Extra["ipsec.local_ip"], info.Extra["ipsec.local_port"] = local[0], local[1]
		info.Extra["ipsec.remote_ip"], info.Extra["ipsec.remote_port"] = remote[0], remote[1]
		info.Extra["ipsec.packet_size"] = p[6]
		info.Message = p[1] + " packet " + p[6] + " bytes " + remote[0]
	}
	if p := childSAPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.child_sa"], info.Extra["ipsec.child_sa_num"], info.Extra["ipsec.child_sa_state"] = p[1], p[2], p[3]
	}
	if p := spiPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.spi_in"], info.Extra["ipsec.spi_out"] = p[1], p[2]
	}
	if p := tsPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.traffic_selectors"] = strings.TrimSpace(p[1])
	}
	if p := proposalPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.proposal"] = p[1]
		info.Message = "selected proposal: " + p[1]
	}
	if p := deletePattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.delete_protocol"], info.Extra["ipsec.delete_spi"] = p[1], p[2]
	}
	if p := authPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.auth_identity"], info.Extra["ipsec.auth_method"], info.Extra["ipsec.auth_result"] = p[1], p[2], p[3]
	}
	if p := ikeSAPattern.FindStringSubmatch(msg); p != nil {
		info.Extra["ipsec.ike_sa_name"], info.Extra["ipsec.ike_sa_num"], info.Extra["ipsec.ike_sa_state"] = p[1], p[2], p[3]
	}
	return info, true
}

// ThreadID reads a thread number: 7 for "07", 0 for "00". Text that is not digits has none.
func ThreadID(s string) (int64, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

var (
	errorLine = regexp.MustCompile(`(?i)\bAUTH_FAILED\b|authentication of .* failed|signature validation failed|\bno (?:shared|private) key found\b|` +
		`\bno trusted .* key found\b|\bno (?:IKE|peer) config found\b|\bgiving up after\b|\bfailed to\b`)
	warnLine = regexp.MustCompile(`(?i)NO_PROPOSAL_CHOSEN|no proposal chosen|didn't accept|\bretransmit\b|\bunable to\b|\bTS_UNACCEPTABLE\b|` +
		`\bINVALID_[A-Z_]+\b|not responding|timed out|\berror\b|\bfail(?:ed|ure)\b`)
)

// Level is the severity word of a message by its text: "error" or "warn" for a line that reports a
// failure, empty for a line the table does not know. It never gives "info".
func Level(msg string) string {
	switch {
	case errorLine.MatchString(msg):
		return "error"
	case warnLine.MatchString(msg):
		return "warn"
	}
	return ""
}
