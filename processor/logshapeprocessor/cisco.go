// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan pmkr

package logshapeprocessor

import (
	"errors"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/cisco"
	"github.com/vladistan/otelcol-custom/shape/rename"
)

// apClock is the device clock a Cisco access point puts before a kernel message, in local time.
var apClock = regexp.MustCompile(`^\[\*?\d{2}/\d{2}/\d{4} \d{2}:\d{2}:\d{2}(?:\.\d+)?\]\s*(.*)$`)

// defaultAPDowngrade are the routine access point kernel messages the default list drops to INFO.
var defaultAPDowngrade = []string{"pmkr0 lookup", "client roamed"}

type ciscoBranch struct {
	cfg       CiscoConfig
	db        *cisco.PortLookup
	downgrade []string
}

func newCiscoBranch(cfg CiscoConfig) (*ciscoBranch, error) {
	b := &ciscoBranch{cfg: cfg, downgrade: defaultAPDowngrade}
	if len(cfg.AccessPointDowngrade) > 0 {
		b.downgrade = make([]string, len(cfg.AccessPointDowngrade))
		for i, s := range cfg.AccessPointDowngrade {
			b.downgrade[i] = strings.ToLower(s)
		}
	}
	if cfg.PortDatabasePath != "" {
		db, err := cisco.LoadPortLookup(cfg.PortDatabasePath)
		if err != nil {
			return nil, err
		}
		b.db = db
	}
	return b, nil
}

func (*ciscoBranch) name() string { return "cisco" }

// source is the raw line: the source attribute, else the body.
func (b *ciscoBranch) source(r *rec) string {
	if v, ok := r.lr.Attributes().Get(b.cfg.SourceAttribute); ok && v.Str() != "" {
		return v.Str()
	}
	return r.body
}

// ciscoLegacy are the names an earlier transform wrote before the vstan prefix.
var ciscoLegacy = []rename.Rule{
	{From: "cisco_facility", To: "vstan.cisco.facility"},
	{From: "cisco_mnemonic", To: "vstan.cisco.mnemonic"},
	{From: "cisco_severity", To: "vstan.cisco.severity"},
}

// claims a record already marked by an earlier parser, or one whose raw line holds a Cisco mnemonic or an HP switch line.
func (b *ciscoBranch) claims(r *rec) bool {
	a := r.lr.Attributes()
	if _, ok := a.Get("vstan.cisco.facility"); ok {
		return true
	}
	if _, ok := a.Get("cisco_facility"); ok {
		return true
	}
	if len(r.body) > 0 && r.body[0] == '[' && apClock.MatchString(r.body) {
		return true
	}
	src := b.source(r)
	if src == "" {
		return false
	}
	if m := cisco.ParseCiscoMnemonic(src); m != nil {
		r.memo = &ciscoMemo{src: src, mnemonic: m}
		return true
	}
	if hp := cisco.ParseHPLog(src); hp != nil {
		r.memo = &ciscoMemo{src: src, hp: hp}
		return true
	}
	return false
}

// ciscoMemo is what claims found in the raw line src: a mnemonic, or else an HP switch line.
type ciscoMemo struct {
	src      string
	mnemonic *cisco.CiscoLogInfo
	hp       *cisco.HPLogInfo
}

// ciscoMnemonic is the mnemonic of src; claims has already looked for it, so a nil there means none.
func ciscoMnemonic(r *rec, src string) *cisco.CiscoLogInfo {
	if kept := memoOf[ciscoMemo](r); kept != nil && kept.src == src {
		return kept.mnemonic
	}
	return cisco.ParseCiscoMnemonic(src)
}

func ciscoHP(r *rec, src string) *cisco.HPLogInfo {
	if kept := memoOf[ciscoMemo](r); kept != nil && kept.src == src && kept.hp != nil {
		return kept.hp
	}
	return cisco.ParseHPLog(src)
}

// accessPoint strips the device clock of an access point kernel line and drops a routine message
// from the device WARN to INFO.
func (b *ciscoBranch) accessPoint(r *rec, text string) {
	a := r.lr.Attributes()
	if _, ok := a.Get(originalKey); !ok {
		a.PutStr(originalKey, r.body)
	}
	r.lr.Body().SetStr(text)
	if r.lr.SeverityNumber() != plog.SeverityNumberWarn {
		return
	}
	lower := strings.ToLower(text)
	for _, s := range b.downgrade {
		if strings.Contains(lower, s) {
			applyLevel(r.lr, wordLevel("info"))
			return
		}
	}
}

func (b *ciscoBranch) parse(r *rec) error {
	if m := apClock.FindStringSubmatch(r.body); m != nil {
		b.accessPoint(r, m[1])
		return nil
	}
	a, res := r.lr.Attributes(), r.res.Attributes()
	rename.ApplyWith(a, ciscoLegacy)
	src := b.source(r)
	hostname := stringAttr(res, hostKey)

	if _, marked := a.Get("vstan.cisco.facility"); marked {
		b.setHostname(res, a)
		b.interfaceName(a, src, hostname)
		b.adjustSeverity(r.lr, a)
		b.cleanBody(r.lr)
		return nil
	}
	if src == "" {
		return errors.New("no source line")
	}

	if info := ciscoMnemonic(r, src); info != nil {
		a.PutStr("vstan.cisco.facility", info.Facility)
		a.PutStr("vstan.cisco.mnemonic", info.Mnemonic)
		a.PutStr("vstan.cisco.severity", info.Severity)
		if _, ok := res.Get(serviceKey); !ok {
			res.PutStr(serviceKey, info.Facility)
		}
		if wlc := cisco.ParseWLCLog(src); wlc != nil {
			b.wlc(r.lr, a, res, wlc)
			return nil
		}
		b.setHostname(res, a)
		if hostname == "" {
			hostname = stringAttr(res, hostKey)
		}
		b.interfaceName(a, src, hostname)
		b.adjustSeverity(r.lr, a)
		b.cleanBody(r.lr)
		return nil
	}
	if info := ciscoHP(r, src); info != nil {
		b.hp(r.lr, a, res, info)
		return nil
	}
	return errors.New("no Cisco or HP pattern")
}

func (b *ciscoBranch) hp(lr plog.LogRecord, a, res pcommon.Map, info *cisco.HPLogInfo) {
	if _, ok := res.Get(hostKey); !ok {
		res.PutStr(hostKey, info.Hostname)
	}
	if _, ok := res.Get(serviceKey); !ok {
		res.PutStr(serviceKey, info.AppName)
	}
	putNonEmpty(a, "thread.name", info.ProcessID)
	putNonEmpty(a, "code.file.path", info.CodeFilepath)
	putInt(a, "code.line.number", info.CodeLineno)
	putNonEmpty(a, "vstan.cisco.sequence", info.LogSequence)
	if b.cfg.RewriteMessages && info.CleanMessage != "" {
		lr.Body().SetStr(info.CleanMessage)
	} else {
		b.cleanBody(lr)
	}
}

func (b *ciscoBranch) wlc(lr plog.LogRecord, a, res pcommon.Map, info *cisco.WLCLogInfo) {
	b.setHostname(res, a)
	putNonEmpty(a, "vstan.wifi.ap.name", info.APName)
	putNonEmpty(a, "thread.name", info.ThreadName)
	putNonEmpty(a, "code.file.path", info.CodeFilepath)
	putInt(a, "code.line.number", info.CodeLineno)
	putNonEmpty(a, "vstan.wifi.client.ap", info.ClientAP)
	putNonEmpty(a, "vstan.wifi.radio", info.Radio)
	putNonEmpty(a, "vstan.wifi.wlan.id", info.WLANId)
	b.adjustSeverity(lr, a)
	if b.cfg.RewriteMessages && info.CleanMessage != "" {
		lr.Body().SetStr(info.CleanMessage)
	} else {
		b.cleanBody(lr)
	}
}

func (b *ciscoBranch) setHostname(res, a pcommon.Map) {
	if !b.cfg.SetHostname {
		return
	}
	if _, ok := res.Get(hostKey); ok {
		return
	}
	if peer, ok := a.Get(peerKey); ok {
		res.PutStr(hostKey, peer.Str())
	}
}

func (b *ciscoBranch) cleanBody(lr plog.LogRecord) {
	if !b.cfg.CleanBody || lr.Body().Type() != pcommon.ValueTypeStr {
		return
	}
	if cleaned := cisco.CleanBody(lr.Body().Str()); cleaned != lr.Body().Str() {
		lr.Body().SetStr(cleaned)
	}
}

func (b *ciscoBranch) interfaceName(a pcommon.Map, src, hostname string) {
	if src == "" {
		return
	}
	name := cisco.ExtractInterfaceName(src)
	if name == "" {
		return
	}
	a.PutStr("network.interface.name", name)
	if b.db != nil && hostname != "" {
		if alias := b.db.Lookup(hostname, name); alias != "" {
			a.PutStr("vstan.cisco.interface_alias", alias)
		}
	}
}

func (b *ciscoBranch) adjustSeverity(lr plog.LogRecord, a pcommon.Map) {
	if !b.cfg.AdjustSeverity {
		return
	}
	mnemonic, ok := a.Get("vstan.cisco.mnemonic")
	if !ok {
		return
	}
	if o := cisco.GetSeverityOverride(mnemonic.Str()); o != nil {
		lr.SetSeverityNumber(o.Number)
		lr.SetSeverityText(o.Text)
		return
	}
	if lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		return
	}
	if sev, ok := a.Get("vstan.cisco.severity"); ok {
		if number, text := cisco.CiscoSeverityToOTEL(sev.Str()); number != plog.SeverityNumberUnspecified {
			lr.SetSeverityNumber(number)
			lr.SetSeverityText(text)
		}
	}
}

func stringAttr(m pcommon.Map, key string) string {
	if v, ok := m.Get(key); ok {
		return v.Str()
	}
	return ""
}
