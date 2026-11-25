// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

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

// apfFacility is the facility of the wireless controller events whose Body carries the client facts.
const apfFacility = "APF"

type ciscoBranch struct {
	cfg       CiscoConfig
	db        *cisco.PortLookup
	downgrade []string
	ssids     map[string]string
}

func newCiscoBranch(cfg CiscoConfig, wlanSSIDs map[string]string) (*ciscoBranch, error) {
	b := &ciscoBranch{cfg: cfg, downgrade: defaultAPDowngrade, ssids: wlanSSIDs}
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
		b.message(r, src)
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
			b.wlc(r.lr, a, res, wlc, info.Facility)
			return nil
		}
		b.setHostname(res, a)
		if hostname == "" {
			hostname = stringAttr(res, hostKey)
		}
		b.interfaceName(a, src, hostname)
		b.adjustSeverity(r.lr, a)
		b.message(r, src)
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

func (b *ciscoBranch) wlc(lr plog.LogRecord, a, res pcommon.Map, info *cisco.WLCLogInfo, facility string) {
	b.setHostname(res, a)
	putNonEmpty(a, "vstan.wifi.ap.name", info.APName)
	putNonEmpty(a, "thread.name", info.ThreadName)
	putNonEmpty(a, "code.file.path", info.CodeFilepath)
	putInt(a, "code.line.number", info.CodeLineno)
	putNonEmpty(a, "vstan.wifi.client.ap", info.ClientAP)
	putNonEmpty(a, "vstan.wifi.radio", info.Radio)
	putNonEmpty(a, "vstan.wifi.wlan.id", info.WLANId)
	b.adjustSeverity(lr, a)
	message := info.CleanMessage
	if facility == apfFacility {
		message = b.apfFacts(a, info.Content)
	}
	if b.cfg.RewriteMessages && message != "" {
		lr.Body().SetStr(message)
	} else {
		b.cleanBody(lr)
	}
}

// apfFacts writes the client, AP, WLAN, SSID and radio facts of an APF event as attributes and returns
// the Body: the event text followed by the facts present. The SSID of the message wins over wlan_ssids.
func (b *ciscoBranch) apfFacts(a pcommon.Map, content string) string {
	w := cisco.ParseWireless(content)
	ssid := w.SSID
	if ssid == "" && w.WLANID != "" {
		ssid = b.ssids[w.WLANID]
	}
	putNonEmpty(a, "client.mac.address", w.ClientMAC)
	putNonEmpty(a, "vstan.wifi.client.ap", w.APName)
	putNonEmpty(a, "vstan.wifi.radio", w.Radio)
	putNonEmpty(a, "vstan.wifi.wlan.id", w.WLANID)
	putNonEmpty(a, "vstan.wifi.ssid", ssid)
	return w.Body(ssid)
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

// message sets the body to what follows the sequence number, device clock and mnemonic of an IOS or
// switch line, and keeps the raw line in log.record.original. Without rewriting, or without such a
// prefix, only the syslog priority goes.
func (b *ciscoBranch) message(r *rec, src string) {
	if b.cfg.RewriteMessages {
		if msg, ok := cisco.StripPrefix(src); ok {
			a := r.lr.Attributes()
			if _, has := a.Get(originalKey); !has {
				a.PutStr(originalKey, src)
			}
			r.lr.Body().SetStr(msg)
			return
		}
	}
	b.cleanBody(r.lr)
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
