// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Buffer kinds the scrubber knows. Any other kind passes through unchanged.
const (
	kindCtrlInfo  = "ctrl_getinfo"
	kindPDList    = "pd_get_list"
	kindPDInfo    = "pd_get_info"
	kindLDInfo    = "ld_get_info"
	kindLDProps   = "ld_get_prop"
	kindCfgRead   = "cfg_read"
	kindBBUDesign = "bbu_get_design_info"
	kindEvents    = "event_get"
)

// Field offsets, from struct layouts in the FreeBSD header mfireg.h (BSD licence), found with
// offsetof on a compiled copy of it.
const (
	ctrlSerialOff = 1424
	ctrlSerialLen = 32

	// Eight SAS port addresses of eight bytes each, for the host side and the device side.
	ctrlHostPortOff   = 40
	ctrlDevicePortOff = 112
	ctrlPortSlots     = 8

	pdInquiryOff   = 4
	pdInquirySkip  = 36 // vendor, product and revision stay; the vendor-specific part holds the serial
	pdInquiryLen   = 96
	pdVPD83Off     = 100
	pdVPD83Len     = 64
	pdVPDHeader    = 4
	pdPathSASOff   = 200
	pdPathSASCount = 4
	pdVPD83ExtOff  = 294
	pdInfoSize     = 358

	pdListHeader = 8
	pdEntrySize  = 24
	pdEntrySAS   = 8

	ldNameOff = 4
	ldNameLen = 16

	// The logical drive info ends in a SCSI VPD page 0x83 of 64 bytes whose designators hold the
	// volume id (an NAA type-6 id of 8 or 16 raw bytes).
	ldVPDOff   = 304
	ldVPDLen   = 64
	vpdPageID  = 0x83
	vpdHeader  = 4
	vpdDesHead = 4

	cfgHeader = 32
	cfgLDName = 4

	bbuSerialOff  = 10
	bbuMfgDataOff = 42
	bbuMfgDataLen = 8
	bbuDesignSize = 50

	evtHeader   = 8
	evtSize     = 256
	evtArgsOff  = 32 // the argument union after the 32-byte event header
	evtArgsSize = 96
	evtDescOff  = 128
	evtDescSize = 128
)

const (
	textMarker = "SCRUB"
	sasMarker  = "SCRB"
	// pseudoAlphabet has no hexadecimal digit, so a token never looks like a WWN.
	pseudoAlphabet = "GHJKLMNPQRSTUVWXYZ"
	minToken       = 6
)

var (
	macPattern = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}\b`)
	wwnPattern = regexp.MustCompile(`\b(?:0[xX])?[0-9A-Fa-f]{16}\b`)
	// wwnSepPattern is a WWN written as eight separated byte pairs.
	wwnSepPattern = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}[:-]){7}[0-9A-Fa-f]{2}\b`)
	// naaPattern is a 128-bit identifier written as 32 hex digits.
	naaPattern  = regexp.MustCompile(`\b[0-9A-Fa-f]{32}\b`)
	naa6Pattern = regexp.MustCompile(`\b6[0-9A-Fa-f]{31}\b`)
	textRun     = regexp.MustCompile(`[\x21-\x7e]{6,}`)
)

// scrubber replaces identifying values with same-length tokens. Equal inputs give equal tokens
// for one salt, so a drive keeps one identity across the files of a bundle.
type scrubber struct {
	salt      []byte
	hostnames []string
	learned   map[string]bool
	// keep makes the scrub idempotent: fields that already hold a token are left as they are,
	// and nothing is learned from the data. Used to rescrub a finished bundle.
	keep bool
}

func newScrubber(salt []byte, hostnames []string) *scrubber {
	s := &scrubber{salt: salt, learned: map[string]bool{}}
	for _, h := range hostnames {
		if h = strings.TrimSpace(h); len(h) >= 3 {
			s.hostnames = append(s.hostnames, h)
		}
	}
	return s
}

func (s *scrubber) digest(orig []byte) [32]byte {
	h := sha256.New()
	h.Write(s.salt)
	h.Write([]byte{0})
	h.Write(orig)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// text returns n letters that start with the marker and depend on orig and the salt.
func (s *scrubber) text(orig []byte, n int) []byte {
	out := make([]byte, n)
	d := s.digest(orig)
	for i := range out {
		if i > 0 && i%len(d) == 0 {
			d = sha256.Sum256(d[:])
		}
		out[i] = pseudoAlphabet[int(d[i%len(d)])%len(pseudoAlphabet)]
	}
	copy(out, textMarker)
	return out
}

// sas returns eight bytes that start with the SAS marker.
func (s *scrubber) sas(orig []byte) []byte {
	d := s.digest(orig)
	out := make([]byte, 8)
	copy(out, sasMarker)
	copy(out[len(sasMarker):], d[:8-len(sasMarker)])
	return out
}

func (s *scrubber) learn(token []byte) {
	if len(token) >= minToken && !s.keep {
		s.learned[string(token)] = true
	}
}

// replaceText overwrites the leading non-NUL bytes of b[off:off+size] with a token of that length.
func (s *scrubber) replaceText(b []byte, off, size int) {
	if off < 0 || off+size > len(b) {
		return
	}
	region := b[off : off+size]
	n := bytes.IndexByte(region, 0)
	if n < 0 {
		n = len(region)
	}
	if n == 0 || s.keep && marked(region[:n]) {
		return
	}
	s.learn(region[:n])
	copy(region[:n], s.text(append([]byte(nil), region[:n]...), n))
}

// replaceFill overwrites a whole region that holds any non-zero byte.
func (s *scrubber) replaceFill(b []byte, off, size int) {
	if off < 0 || off+size > len(b) || allZero(b[off:off+size]) {
		return
	}
	region := b[off : off+size]
	if s.keep && bytes.HasPrefix(region, []byte(textMarker)) {
		return
	}
	for _, run := range textRun.FindAll(region, -1) {
		s.learn(run)
	}
	copy(region, s.text(append([]byte(nil), region...), size))
}

func (s *scrubber) replaceSAS(b []byte, off int) {
	if off < 0 || off+8 > len(b) || allZero(b[off:off+8]) {
		return
	}
	if s.keep && bytes.HasPrefix(b[off:off+8], []byte(sasMarker)) {
		return
	}
	copy(b[off:off+8], s.sas(append([]byte(nil), b[off:off+8]...)))
}

// scrub returns a copy of data with the identifying fields of the kind replaced.
func (s *scrubber) scrub(kind string, data []byte) []byte {
	b := append([]byte(nil), data...)
	switch kind {
	case kindCtrlInfo:
		s.replaceText(b, ctrlSerialOff, ctrlSerialLen)
		for _, base := range []int{ctrlHostPortOff, ctrlDevicePortOff} {
			for i := 0; i < ctrlPortSlots; i++ {
				s.replaceSAS(b, base+8*i)
			}
		}
	case kindPDList:
		s.scrubPDList(b)
	case kindPDInfo:
		s.scrubPDInfo(b)
	case kindLDInfo, kindLDProps:
		s.replaceText(b, ldNameOff, ldNameLen)
		if kind == kindLDInfo {
			s.scrubLDVPD(b)
		}
	case kindCfgRead:
		s.scrubConfig(b)
	case kindBBUDesign:
		s.scrubBattery(b)
	case kindEvents:
		s.scrubEvents(b)
	}
	return b
}

func (s *scrubber) scrubPDList(b []byte) {
	if len(b) < pdListHeader {
		return
	}
	n := int(min(binary.LittleEndian.Uint32(b[4:]), uint32(len(b))))
	for i := 0; i < n && pdListHeader+(i+1)*pdEntrySize <= len(b); i++ {
		base := pdListHeader + i*pdEntrySize + pdEntrySAS
		s.replaceSAS(b, base)
		s.replaceSAS(b, base+8)
	}
}

func (s *scrubber) scrubPDInfo(b []byte) {
	if len(b) < pdInfoSize {
		return
	}
	s.replaceFill(b, pdInquiryOff+pdInquirySkip, pdInquiryLen-pdInquirySkip)
	s.replaceFill(b, pdVPD83Off+pdVPDHeader, pdVPD83Len-pdVPDHeader)
	s.replaceFill(b, pdVPD83ExtOff+pdVPDHeader, pdVPD83Len-pdVPDHeader)
	for i := 0; i < pdPathSASCount; i++ {
		s.replaceSAS(b, pdPathSASOff+8*i)
	}
}

func (s *scrubber) scrubConfig(b []byte) {
	if len(b) < cfgHeader {
		return
	}
	le := binary.LittleEndian
	arrays, arraySize := int(le.Uint16(b[4:])), int(le.Uint16(b[6:]))
	lds, ldSize := int(le.Uint16(b[8:])), int(le.Uint16(b[10:]))
	if ldSize < cfgLDName+ldNameLen {
		return
	}
	for i := 0; i < lds; i++ {
		off := cfgHeader + arrays*arraySize + i*ldSize
		if off+cfgLDName+ldNameLen > len(b) {
			return
		}
		s.replaceText(b, off+cfgLDName, ldNameLen)
	}
}

func (s *scrubber) scrubBattery(b []byte) {
	if len(b) < bbuDesignSize {
		return
	}
	if !s.keep { // a two-byte number cannot show whether it was replaced already
		orig := binary.LittleEndian.Uint16(b[bbuSerialOff:])
		d := s.digest(b[bbuSerialOff : bbuSerialOff+2])
		repl := binary.LittleEndian.Uint16(d[:2])
		if repl == orig {
			repl ^= 0xFFFF
		}
		binary.LittleEndian.PutUint16(b[bbuSerialOff:], repl)
	}
	s.replaceFill(b, bbuMfgDataOff, bbuMfgDataLen)
}

func (s *scrubber) scrubEvents(b []byte) {
	if len(b) < evtHeader {
		return
	}
	n := int(min(binary.LittleEndian.Uint32(b[0:]), uint32(len(b))))
	for i := 0; i < n; i++ {
		base := evtHeader + i*evtSize
		if base+evtDescOff+evtDescSize > len(b) {
			return
		}
		// The argument union can hold text or sense data; keep only the marker.
		args := b[base+evtArgsOff : base+evtArgsOff+evtArgsSize]
		clear(args)
		copy(args, textMarker)
		s.scrubFreeText(b[base+evtDescOff : base+evtDescOff+evtDescSize])
	}
}

// scrubFreeText replaces, in place, what an event text can name: this host, serials seen earlier
// in the run, MAC addresses and WWNs. Each token keeps the length of what it replaces.
func (s *scrubber) scrubFreeText(b []byte) {
	for _, h := range s.hostnames {
		replaceAll(b, []byte(h), s.text)
	}
	toks := make([]string, 0, len(s.learned))
	for tok := range s.learned {
		toks = append(toks, tok)
	}
	// Longest first, then by value, so overlapping strings give the same output on every run.
	sort.Slice(toks, func(i, j int) bool {
		if len(toks[i]) != len(toks[j]) {
			return len(toks[i]) > len(toks[j])
		}
		return toks[i] < toks[j]
	})
	for _, tok := range toks {
		replaceAll(b, []byte(tok), s.text)
	}
	for _, re := range []*regexp.Regexp{wwnSepPattern, macPattern, naaPattern, wwnPattern} {
		for _, loc := range re.FindAllIndex(b, -1) {
			copy(b[loc[0]:loc[1]], s.text(append([]byte(nil), b[loc[0]:loc[1]]...), loc[1]-loc[0]))
		}
	}
	for _, r := range alnumRuns(b) {
		if serialShaped(b[r[0]:r[1]]) {
			copy(b[r[0]:r[1]], s.text(append([]byte(nil), b[r[0]:r[1]]...), r[1]-r[0]))
		}
	}
}

// scrubLDVPD replaces the data of every designator in the VPD page of a logical drive.
func (s *scrubber) scrubLDVPD(b []byte) {
	if ldVPDOff+vpdHeader > len(b) {
		return
	}
	page := b[ldVPDOff:min(ldVPDOff+ldVPDLen, len(b))]
	if allZero(page) {
		return
	}
	if page[1] != vpdPageID {
		s.replaceFill(b, ldVPDOff, len(page))
		return
	}
	for _, d := range designators(page) {
		if d[1] > d[0] && !allZero(page[d[0]:d[1]]) && !(s.keep && marked(page[d[0]:d[1]])) {
			copy(page[d[0]:d[1]], s.text(append([]byte(nil), page[d[0]:d[1]]...), d[1]-d[0]))
		}
	}
}

// designators returns the [start,end) data ranges of the designators in a VPD 0x83 page.
func designators(page []byte) [][2]int {
	var out [][2]int
	end := min(len(page), vpdHeader+int(binary.BigEndian.Uint16(page[2:])))
	for i := vpdHeader; i+vpdDesHead <= end; {
		start := i + vpdDesHead
		stop := min(start+int(page[i+3]), end)
		out = append(out, [2]int{start, stop})
		i = start + int(page[i+3])
	}
	return out
}

// alnumRuns returns the [start,end) ranges of the runs of ASCII letters and digits.
func alnumRuns(b []byte) [][2]int {
	var out [][2]int
	for i := 0; i < len(b); {
		if !isAlnum(b[i]) {
			i++
			continue
		}
		j := i
		for j < len(b) && isAlnum(b[j]) {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// serialShaped is the rule for an identifier inside event text. A run of letters and digits is
// serial-shaped when it has 8 or more characters and either mixes digits with letters, or is all
// digits and has 11 or more (a 32-bit count has at most 10). Plain words, short numbers and
// hex numbers written 0x plus up to 8 digits stay. Hyphens end a run.
func serialShaped(tok []byte) bool {
	if len(tok) < 8 || bytes.HasPrefix(tok, []byte(textMarker)) {
		return false
	}
	digits, letters := 0, 0
	for _, c := range tok {
		if c >= '0' && c <= '9' {
			digits++
		} else {
			letters++
		}
	}
	if digits == 0 {
		return false
	}
	if letters == 0 {
		return len(tok) >= 11
	}
	if (tok[1] == 'x' || tok[1] == 'X') && tok[0] == '0' && len(tok) <= 10 && isHex(tok[2:]) {
		return false
	}
	return true
}

func isHex(b []byte) bool {
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// highEntropy reports a run of 8 or more letters and digits that looks random: Shannon entropy of
// 3.0 bits per character or more, or 3.5 for a plain word (one case, or one capital then lower
// case). Scrub tokens never count.
func highEntropy(tok []byte) bool {
	if len(tok) < 8 || bytes.HasPrefix(tok, []byte(textMarker)) {
		return false
	}
	var count [256]int
	for _, c := range tok {
		count[c]++
	}
	h := 0.0
	for _, n := range count {
		if n > 0 {
			p := float64(n) / float64(len(tok))
			h -= p * math.Log2(p)
		}
	}
	if plainWord(tok) {
		return h >= 3.5
	}
	return h >= 3.0
}

func plainWord(tok []byte) bool {
	upper, lower := 0, 0
	for i, c := range tok {
		switch {
		case c >= 'A' && c <= 'Z':
			if i > 0 {
				upper++
			}
		case c >= 'a' && c <= 'z':
			lower++
		default:
			return false
		}
	}
	return upper == 0 || lower == 0
}

func replaceAll(b, old []byte, token func([]byte, int) []byte) {
	for i := 0; i+len(old) <= len(b); {
		j := bytes.Index(b[i:], old)
		if j < 0 {
			return
		}
		at := i + j
		copy(b[at:at+len(old)], token(append([]byte(nil), old...), len(old)))
		i = at + len(old)
	}
}

// marked reports whether a text field is empty or starts with the scrub marker.
func marked(region []byte) bool {
	if n := bytes.IndexByte(region, 0); n >= 0 {
		region = region[:n]
	}
	if len(region) == 0 {
		return true
	}
	if len(region) < len(textMarker) {
		return strings.HasPrefix(textMarker, string(region))
	}
	return bytes.HasPrefix(region, []byte(textMarker))
}

func sasMarked(b []byte) bool { return allZero(b) || bytes.HasPrefix(b, []byte(sasMarker)) }

// verifyBuffer lists what is still identifying in a buffer of the kind: fields without the scrub
// marker, and any MAC address or WWN anywhere in the bytes.
func verifyBuffer(kind string, b []byte) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	// Findings name a category and an offset, never the value.
	for _, f := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"a MAC address is present", macPattern},
		{"a WWN is present", wwnPattern},
		{"a WWN with separators is present", wwnSepPattern},
		{"an NAA-6 identifier is present", naa6Pattern},
	} {
		for _, loc := range f.re.FindAllIndex(b, -1) {
			add("%s at offset %d", f.name, loc[0])
		}
	}
	field := func(off, size int) []byte {
		if off < 0 || off+size > len(b) {
			return nil
		}
		return b[off : off+size]
	}
	switch kind {
	case kindCtrlInfo:
		if r := field(ctrlSerialOff, ctrlSerialLen); r != nil && !marked(r) {
			add("the controller serial is not scrubbed")
		}
		for _, base := range []int{ctrlHostPortOff, ctrlDevicePortOff} {
			for i := 0; i < ctrlPortSlots; i++ {
				if f := field(base+8*i, 8); f != nil && !sasMarked(f) {
					add("a controller SAS port address is not scrubbed")
					break
				}
			}
		}
	case kindEvents:
		verifyEvents(b, add)
	case kindPDList:
		verifyPDList(b, add)
	case kindPDInfo:
		for _, r := range []struct {
			name      string
			off, size int
		}{
			{"drive inquiry serial", pdInquiryOff + pdInquirySkip, pdInquiryLen - pdInquirySkip},
			{"drive VPD page", pdVPD83Off + pdVPDHeader, pdVPD83Len - pdVPDHeader},
			{"drive VPD extension", pdVPD83ExtOff + pdVPDHeader, pdVPD83Len - pdVPDHeader},
		} {
			if f := field(r.off, r.size); f != nil && !allZero(f) && !bytes.HasPrefix(f, []byte(textMarker)) {
				add("the %s is not scrubbed", r.name)
			}
		}
		for i := 0; i < pdPathSASCount; i++ {
			if f := field(pdPathSASOff+8*i, 8); f != nil && !sasMarked(f) {
				add("a drive SAS address is not scrubbed")
			}
		}
	case kindLDInfo, kindLDProps:
		if r := field(ldNameOff, ldNameLen); r != nil && !marked(r) {
			add("the logical drive name is not scrubbed at offset %d", ldNameOff)
		}
		if kind == kindLDInfo {
			verifyLDVPD(b, add)
		}
	case kindCfgRead:
		verifyConfig(b, add)
	case kindBBUDesign:
		if r := field(bbuMfgDataOff, bbuMfgDataLen); r != nil && !allZero(r) && !bytes.HasPrefix(r, []byte(textMarker)) {
			add("the battery manufacturer data is not scrubbed")
		}
	}
	return problems
}

func verifyPDList(b []byte, add func(string, ...any)) {
	if len(b) < pdListHeader {
		return
	}
	n := int(min(binary.LittleEndian.Uint32(b[4:]), uint32(len(b))))
	for i := 0; i < n && pdListHeader+(i+1)*pdEntrySize <= len(b); i++ {
		base := pdListHeader + i*pdEntrySize + pdEntrySAS
		if !sasMarked(b[base:base+8]) || !sasMarked(b[base+8:base+16]) {
			add("a SAS address in the drive list is not scrubbed")
			return
		}
	}
}

// verifyLDVPD flags every designator of the logical drive VPD page that is not scrubbed.
func verifyLDVPD(b []byte, add func(string, ...any)) {
	if ldVPDOff+vpdHeader > len(b) {
		return
	}
	page := b[ldVPDOff:min(ldVPDOff+ldVPDLen, len(b))]
	if allZero(page) {
		return
	}
	if page[1] != vpdPageID {
		if !bytes.HasPrefix(page, []byte(textMarker)) {
			add("the logical drive VPD page is not scrubbed at offset %d", ldVPDOff)
		}
		return
	}
	for _, d := range designators(page) {
		data := page[d[0]:d[1]]
		if len(data) == 0 || allZero(data) {
			continue
		}
		if len(data) < len(textMarker) && bytes.HasPrefix([]byte(textMarker), data) || bytes.HasPrefix(data, []byte(textMarker)) {
			continue
		}
		if data[0]>>4 == 6 {
			add("an NAA-6 volume id is not scrubbed at offset %d", ldVPDOff+d[0])
		} else {
			add("a volume designator is not scrubbed at offset %d", ldVPDOff+d[0])
		}
	}
}

// verifyEvents checks that the argument union of every event holds nothing but the marker, and
// that no description holds a serial-shaped or high-entropy token.
func verifyEvents(b []byte, add func(string, ...any)) {
	if len(b) < evtHeader {
		return
	}
	n := int(min(binary.LittleEndian.Uint32(b[0:]), uint32(len(b))))
	for i := 0; i < n; i++ {
		base := evtHeader + i*evtSize
		if base+evtDescOff+evtDescSize > len(b) {
			return
		}
		args := b[base+evtArgsOff : base+evtArgsOff+evtArgsSize]
		if !allZero(args) && (!bytes.HasPrefix(args, []byte(textMarker)) || !allZero(args[len(textMarker):])) {
			add("the argument bytes of an event are not scrubbed at offset %d", base+evtArgsOff)
			return
		}
		desc := b[base+evtDescOff : base+evtDescOff+evtDescSize]
		for _, r := range alnumRuns(desc) {
			switch tok := desc[r[0]:r[1]]; {
			case serialShaped(tok):
				add("a serial-shaped token is in event text at offset %d", base+evtDescOff+r[0])
			case highEntropy(tok):
				add("a high-entropy token is in event text at offset %d", base+evtDescOff+r[0])
			}
		}
	}
}

func verifyConfig(b []byte, add func(string, ...any)) {
	if len(b) < cfgHeader {
		return
	}
	le := binary.LittleEndian
	arrays, arraySize := int(le.Uint16(b[4:])), int(le.Uint16(b[6:]))
	lds, ldSize := int(le.Uint16(b[8:])), int(le.Uint16(b[10:]))
	if ldSize < cfgLDName+ldNameLen {
		return
	}
	for i := 0; i < lds; i++ {
		off := cfgHeader + arrays*arraySize + i*ldSize + cfgLDName
		if off+ldNameLen > len(b) {
			return
		}
		if !marked(b[off : off+ldNameLen]) {
			add("a logical drive name in the configuration is not scrubbed")
			return
		}
	}
}
