// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/syslog"
)

const (
	hostKey     = "host.name"
	serviceKey  = "service.name" // pragma: allowlist secret
	peerKey     = "net.peer.name"
	originalKey = "log.record.original"
)

type syslogBranch struct{ cfg SyslogConfig }

func newSyslogBranch(cfg SyslogConfig) *syslogBranch { return &syslogBranch{cfg: cfg} }

func (*syslogBranch) name() string { return "syslog" }

// claims a record that carries the raw line in the source attribute, a syslog priority, or a syslog-shaped body.
func (b *syslogBranch) claims(r *rec) bool {
	a := r.lr.Attributes()
	if v, ok := a.Get(b.cfg.SourceAttribute); ok && v.Str() != "" {
		return true
	}
	if _, ok := a.Get("priority"); ok {
		return true
	}
	format := syslog.DetectFormat(r.body)
	if format != syslog.FormatUnknown {
		r.memo = &syslogMemo{text: r.body, format: format}
	}
	return format != syslog.FormatUnknown
}

// syslogMemo is the format claims found in the body, for parse to use when it parses that same text.
type syslogMemo struct {
	text   string
	format syslog.SyslogFormat
}

func (b *syslogBranch) parse(r *rec) error {
	a := r.lr.Attributes()
	message, regexFailed := "", false
	if v, ok := a.Get(b.cfg.SourceAttribute); ok {
		message = v.Str()
	}
	if message == "" && r.body != "" {
		message, regexFailed = r.body, true
	}
	if message == "" {
		return errors.New("no syslog line")
	}
	if regexFailed {
		a.PutBool("vstan.syslog.parse_error", true)
	}
	if b.cfg.PreserveOriginal {
		if _, exists := a.Get(originalKey); !exists {
			a.PutStr(originalKey, message)
		}
	}

	var info *syslog.SyslogInfo
	if kept := memoOf[syslogMemo](r); kept != nil && kept.text == message {
		info = syslog.ParseAs(message, kept.format)
	} else {
		info = syslog.Parse(message)
	}
	if info == nil {
		b.mapSeverity(r.lr)
		a.PutStr("log.source", "syslog")
		return nil
	}
	if info.Format == syslog.FormatRFC3164 && syslog.IsEmbeddedRFC5424(info.Content) {
		if inner := syslog.Parse(info.Content); inner != nil {
			if inner.Appname != "" {
				info.Appname = inner.Appname
			}
			if inner.ProcID != "" {
				info.ProcID = inner.ProcID
			}
			info.Content = inner.Content
			if inner.Hostname != "" {
				info.Hostname = inner.Hostname
			}
		}
	}

	res := r.res.Attributes()
	host := info.Hostname
	if host == "" && info.HostMissing {
		// No HOSTNAME in the line: the sender is the host the receiver saw.
		if peer, ok := a.Get(peerKey); ok {
			host = peer.Str()
		} else if peer, ok := res.Get(peerKey); ok {
			host = peer.Str()
		}
	}
	if b.cfg.SetHostname {
		switch {
		case host != "":
			res.PutStr(hostKey, host)
		case info.HostMissing:
			res.Remove(hostKey)
		}
	}
	if b.cfg.SetServiceName && info.Appname != "" {
		res.PutStr(serviceKey, info.Appname)
	}
	if info.ProcID != "" {
		putInt(a, "process.pid", info.ProcID)
	}
	if info.Format == syslog.FormatCiscoIOS {
		putNonEmpty(a, "vstan.cisco.facility", info.CiscoFacility)
		putNonEmpty(a, "vstan.cisco.mnemonic", info.CiscoMnemonic)
		putNonEmpty(a, "vstan.cisco.sequence", info.CiscoSeq)
	}
	if ip, ok := a.Get("net.peer.ip"); ok {
		a.PutStr("source.address", ip.Str())
	}
	b.mapSeverity(r.lr)
	if b.cfg.CleanBody && info.Content != "" {
		r.lr.Body().SetStr(info.Content)
	}
	if !info.Time.IsZero() && r.lr.Timestamp() == 0 {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(info.Time))
	}
	a.PutStr("log.source", "syslog")
	return nil
}

func (b *syslogBranch) mapSeverity(lr plog.LogRecord) {
	if !b.cfg.MapSeverity {
		return
	}
	pri, ok := lr.Attributes().Get("priority")
	if !ok {
		return
	}
	if number, text, ok := syslog.MapPriorityToSeverity(pri.Str()); ok {
		lr.SetSeverityNumber(number)
		lr.SetSeverityText(text)
	}
}

func putNonEmpty(a pcommon.Map, key, value string) {
	if value != "" {
		a.PutStr(key, value)
	}
}

// putInt stores a number as an integer attribute and anything else as written.
func putInt(a pcommon.Map, key, value string) {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		a.PutInt(key, n)
		return
	}
	a.PutStr(key, value)
}
