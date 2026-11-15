// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	eventConnect    = "connect"
	eventAuth       = "auth"
	eventDisconnect = "disconnect"
	eventLifecycle  = "lifecycle"
	eventError      = "error"
	eventOther      = "other"
)

const (
	connectionPrefix = "Incoming rfb connection from "
	serverStarted    = "Rfb server started at "
	viewOnlyPrefix   = "Authenticated with view-only password = "
	clientTag        = "Client #"
)

// hasErrorWord is the one definition of error text, shared by severity and the noise-prefix exemption.
func hasErrorWord(msg string) bool {
	return strings.HasPrefix(msg, "Exception") || containsFold(msg, "error") || containsFold(msg, "failed")
}

func severityOf(msg string) (plog.SeverityNumber, string) {
	switch {
	case hasErrorWord(msg):
		return plog.SeverityNumberError, "ERROR"
	case containsFold(msg, "rejected"), containsFold(msg, "denied"),
		containsFold(msg, "refused"), containsFold(msg, "warning"):
		return plog.SeverityNumberWarn, "WARN"
	}
	return plog.SeverityNumberInfo, "INFO"
}

// eventOf checks the vocabularies from most to least specific: "disconnected" holds "connected",
// and a failed authentication is an auth event that also reads as an error.
func eventOf(msg string, marked bool) string {
	switch {
	case containsFold(msg, "disconnect"):
		return eventDisconnect
	case containsFold(msg, "authenticat"):
		return eventAuth
	case containsFold(msg, "connection from"), containsFold(msg, "connected"):
		return eventConnect
	case hasErrorWord(msg):
		return eventError
	case marked:
		return eventLifecycle
	}
	return eventOther
}

// containsFold reports whether s holds sub, ignoring ASCII case; sub must be lower case.
// Noise lines reach it, so it avoids the allocation strings.ToLower would make.
func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		j := 0
		for ; j < len(sub); j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				break
			}
		}
		if j == len(sub) {
			return true
		}
	}
	return false
}

// describe sets the attributes the message text allows; a value that does not parse is left out.
func describe(attrs pcommon.Map, msg string) {
	if rest, ok := strings.CutPrefix(msg, connectionPrefix); ok {
		address, port, _ := strings.Cut(rest, " to port ")
		attrs.PutStr("client.address", address)
		putPort(attrs, port)
	}
	if i := strings.Index(msg, clientTag); i >= 0 {
		if id, _, ok := scanInt(msg, i+len(clientTag)); ok {
			attrs.PutInt("tightvnc.client.id", id)
		}
	}
	if rest, ok := strings.CutPrefix(msg, viewOnlyPrefix); ok {
		if flag, _, ok := scanInt(rest, 0); ok {
			attrs.PutInt("tightvnc.view_only", flag)
		}
	}
	if rest, ok := strings.CutPrefix(msg, serverStarted); ok {
		if i := strings.LastIndexByte(rest, ':'); i >= 0 {
			attrs.PutStr("server.address", rest[:i])
			putPort(attrs, rest[i+1:])
		}
	}
}

func putPort(attrs pcommon.Map, text string) {
	if port, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32); err == nil {
		attrs.PutInt("server.port", port)
	}
}
