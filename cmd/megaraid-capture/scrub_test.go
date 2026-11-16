// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func testScrubber() *scrubber {
	return newScrubber([]byte("test-salt"), []string{rawHostname})
}

func TestScrubKeepsEveryBufferLength(t *testing.T) {
	s := testScrubber()
	buffers := map[string][]byte{
		kindCtrlInfo:     ctrlInfoBuf(rawCtrlSerial, true),
		kindPDList:       pdListBuf([]uint16{0, 8}),
		kindPDInfo:       pdInfoBuf(0, rawDriveSN0),
		kindLDInfo:       ldInfoBuf(rawLDName),
		kindLDProps:      ldPropsBuf(rawLDName),
		kindCfgRead:      cfgReadBuf(rawLDName),
		kindBBUDesign:    bbuDesignBuf(),
		kindEvents:       eventListBuf(1, eventTexts()),
		"ctrl_get_props": make([]byte, 64),
	}
	for kind, raw := range buffers {
		if got := s.scrub(kind, raw); len(got) != len(raw) {
			t.Errorf("%s: length %d became %d", kind, len(raw), len(got))
		}
	}
}

func TestScrubDoesNotChangeTheInput(t *testing.T) {
	raw := ctrlInfoBuf(rawCtrlSerial, true)
	keep := append([]byte(nil), raw...)

	testScrubber().scrub(kindCtrlInfo, raw)

	if !bytes.Equal(raw, keep) {
		t.Fatal("scrub modified its argument")
	}
}

func TestScrubCtrlInfoReplacesTheSerialAndKeepsTheRest(t *testing.T) {
	raw := ctrlInfoBuf(rawCtrlSerial, true)

	got := testScrubber().scrub(kindCtrlInfo, raw)

	serial := strings.TrimRight(string(got[1424:1456]), "\x00")
	if len(serial) != len(rawCtrlSerial) || !strings.HasPrefix(serial, "SCRUB") || serial == rawCtrlSerial {
		t.Fatalf("serial = %q", serial)
	}
	if !bytes.Equal(got[:40], raw[:40]) || !bytes.Equal(got[176:1424], raw[176:1424]) || !bytes.Equal(got[1456:], raw[1456:]) {
		t.Fatal("bytes outside the serial and the port addresses changed")
	}
	if !strings.Contains(string(got), rawProduct) {
		t.Fatal("the product name must stay")
	}
}

func TestScrubCtrlInfoReplacesPortAddressesAndKeepsZeroSlots(t *testing.T) {
	raw := ctrlInfoBuf(rawCtrlSerial, true)

	got := testScrubber().scrub(kindCtrlInfo, raw)

	for _, off := range []int{40, 112, 120} {
		if !bytes.HasPrefix(got[off:off+8], []byte(sasMarker)) {
			t.Errorf("port address at %d = %x", off, got[off:off+8])
		}
	}
	if !bytes.Equal(got[48:56], make([]byte, 8)) || !bytes.Equal(got[128:176], make([]byte, 48)) {
		t.Error("zero slots must stay zero")
	}
	if !bytes.Equal(got[112:120], got[40:48]) {
		t.Error("one address must give one token")
	}
}

func TestScrubEventsReplacesTheArgumentBytesAndKeepsTheHeader(t *testing.T) {
	raw := eventListBuf(500, eventTexts())

	got := testScrubber().scrub(kindEvents, raw)

	for i := 0; i < 3; i++ {
		e, r := got[8+256*i:], raw[8+256*i:]
		if !bytes.Equal(e[:32], r[:32]) {
			t.Errorf("event %d: header changed", i)
		}
		if !bytes.HasPrefix(e[32:128], []byte("SCRUB")) || !allZero(e[37:128]) {
			t.Errorf("event %d: args = %q", i, e[32:128])
		}
	}
	if bytes.Contains(got, []byte(rawEventArgs)) {
		t.Error("the argument text survived")
	}
}

func TestVerifyFlagsUnscrubbedPortAddressesAndEventArguments(t *testing.T) {
	s := testScrubber()
	ctrl := ctrlInfoBuf(rawCtrlSerial, true)
	events := eventListBuf(1, eventTexts())

	if p := verifyBuffer(kindCtrlInfo, s.scrub(kindCtrlInfo, ctrl)); len(p) != 0 {
		t.Errorf("scrubbed ctrl info: %v", p)
	}
	rawAddr := append([]byte(nil), s.scrub(kindCtrlInfo, ctrl)...)
	copy(rawAddr[112:], rawSAS[1][:])
	if p := verifyBuffer(kindCtrlInfo, rawAddr); len(p) == 0 {
		t.Error("a raw device port address passed verify")
	}
	if p := verifyBuffer(kindEvents, s.scrub(kindEvents, events)); len(p) != 0 {
		t.Errorf("scrubbed events: %v", p)
	}
	rawArgs := append([]byte(nil), s.scrub(kindEvents, events)...)
	copy(rawArgs[8+32:], rawEventArgs)
	if p := verifyBuffer(kindEvents, rawArgs); len(p) == 0 {
		t.Error("raw event arguments passed verify")
	}
}

func TestScrubIsStableWithinASaltAndDiffersAcrossSalts(t *testing.T) {
	raw := ctrlInfoBuf(rawCtrlSerial, true)

	a := newScrubber([]byte("one"), nil).scrub(kindCtrlInfo, raw)
	b := newScrubber([]byte("one"), nil).scrub(kindCtrlInfo, raw)
	c := newScrubber([]byte("two"), nil).scrub(kindCtrlInfo, raw)
	d := newScrubber([]byte("one"), nil).scrub(kindCtrlInfo, ctrlInfoBuf("SV99999999", true))

	if !bytes.Equal(a, b) {
		t.Error("the same salt and value must give the same output")
	}
	if bytes.Equal(a, c) {
		t.Error("another salt must give another output")
	}
	if bytes.Equal(a[1424:1456], d[1424:1456]) {
		t.Error("another serial must give another token")
	}
}

func TestScrubPDInfoKeepsVendorAndModelAndReplacesIdentity(t *testing.T) {
	raw := pdInfoBuf(8, rawDriveSN0)

	got := testScrubber().scrub(kindPDInfo, raw)

	if !bytes.Equal(got[4:4+36], raw[4:4+36]) {
		t.Error("vendor, product and revision must stay")
	}
	if bytes.Equal(got[4+36:4+96], raw[4+36:4+96]) || !bytes.HasPrefix(got[4+36:], []byte("SCRUB")) {
		t.Errorf("the serial region was not replaced: %q", got[4+36:4+60])
	}
	if !bytes.HasPrefix(got[100+4:], []byte("SCRUB")) || !bytes.HasPrefix(got[294+4:], []byte("SCRUB")) {
		t.Error("the VPD identifiers were not replaced")
	}
	for _, off := range []int{200, 208} {
		if !bytes.HasPrefix(got[off:off+8], []byte("SCRB")) {
			t.Errorf("sas address at %d = %x", off, got[off:off+8])
		}
	}
	if !bytes.Equal(got[216:224], make([]byte, 8)) || !bytes.Equal(got[224:232], make([]byte, 8)) {
		t.Error("an empty sas address must stay zero")
	}
	if binary.LittleEndian.Uint32(got[168:]) != 3 || got[192] != 1 || binary.LittleEndian.Uint16(got[186:]) != 0x18 {
		t.Error("counters and state must stay")
	}
}

func TestScrubPDListReplacesSasAddressesOfEveryEntry(t *testing.T) {
	raw := pdListBuf([]uint16{0, 8, 9})

	got := testScrubber().scrub(kindPDList, raw)

	for i := 0; i < 3; i++ {
		e := got[8+24*i:]
		if binary.LittleEndian.Uint16(e) != []uint16{0, 8, 9}[i] {
			t.Errorf("entry %d lost its device id", i)
		}
		if !bytes.HasPrefix(e[8:16], []byte("SCRB")) {
			t.Errorf("entry %d sas address = %x", i, e[8:16])
		}
		if !bytes.Equal(e[16:24], make([]byte, 8)) {
			t.Errorf("entry %d second address must stay zero", i)
		}
	}
	if !bytes.Equal(got[8+24*3:], raw[8+24*3:]) {
		t.Error("bytes after the last entry changed")
	}
}

func TestScrubPDListWithACountBeyondTheBufferDoesNotPanic(t *testing.T) {
	raw := pdListBuf([]uint16{0})
	binary.LittleEndian.PutUint32(raw[4:], 0xFFFFFFFF)

	got := testScrubber().scrub(kindPDList, raw)

	if len(got) != len(raw) {
		t.Fatal("length changed")
	}
}

func TestScrubSameAddressGivesTheSameReplacement(t *testing.T) {
	s := testScrubber()
	list := s.scrub(kindPDList, pdListBuf([]uint16{0}))
	info := s.scrub(kindPDInfo, pdInfoBuf(0, rawDriveSN0))

	if !bytes.Equal(list[16:24], info[200:208]) {
		t.Fatalf("one address, two tokens: %x and %x", list[16:24], info[200:208])
	}
}

func TestScrubLogicalDriveNames(t *testing.T) {
	s := testScrubber()
	for kind, raw := range map[string][]byte{kindLDInfo: ldInfoBuf(rawLDName), kindLDProps: ldPropsBuf(rawLDName)} {
		got := s.scrub(kind, raw)

		name := strings.TrimRight(string(got[4:20]), "\x00")
		if strings.Contains(name, "labhost") || !strings.HasPrefix(name, "SCRUB") || len(name) != len(rawLDName) {
			t.Errorf("%s: name = %q", kind, name)
		}
		if !bytes.Equal(got[20:min(304, len(got))], raw[20:min(304, len(raw))]) || len(got) > 368 && !bytes.Equal(got[368:], raw[368:]) {
			t.Errorf("%s: bytes after the name changed", kind)
		}
	}
}

func TestScrubConfigReplacesTheNamesOfEveryLogicalDrive(t *testing.T) {
	raw := cfgReadBuf(rawLDName)

	got := testScrubber().scrub(kindCfgRead, raw)

	if bytes.Contains(got, []byte("labhost")) {
		t.Fatal("the logical drive name survived")
	}
	if !bytes.Equal(got[:32], raw[:32]) {
		t.Error("the header changed")
	}
}

func TestScrubConfigWithBadHeaderCountsDoesNotPanic(t *testing.T) {
	raw := cfgReadBuf(rawLDName)
	binary.LittleEndian.PutUint16(raw[4:], 0xFFFF)
	binary.LittleEndian.PutUint16(raw[6:], 0xFFFF)
	binary.LittleEndian.PutUint16(raw[8:], 0xFFFF)
	binary.LittleEndian.PutUint16(raw[10:], 0xFFFF)

	got := testScrubber().scrub(kindCfgRead, raw)

	if len(got) != len(raw) {
		t.Fatal("length changed")
	}
}

func TestScrubBatteryDesignInfo(t *testing.T) {
	raw := bbuDesignBuf()

	got := testScrubber().scrub(kindBBUDesign, raw)

	if binary.LittleEndian.Uint16(got[10:]) == rawBBUSerial {
		t.Error("the battery serial stayed")
	}
	if bytes.Contains(got, []byte("20140921")) {
		t.Error("the manufacturer data stayed")
	}
	if !bytes.Equal(got[14:42], raw[14:42]) {
		t.Error("the manufacturer name and device name must stay")
	}
}

func TestScrubEventsReplacesWWNsMACsHostnamesAndKnownSerials(t *testing.T) {
	s := testScrubber()
	s.scrub(kindCtrlInfo, ctrlInfoBuf(rawCtrlSerial, true))
	raw := eventListBuf(500, eventTexts())

	got := s.scrub(kindEvents, raw)

	for _, v := range []string{rawWWN, rawMAC, rawHostname, rawCtrlSerial} {
		if bytes.Contains(got, []byte(v)) {
			t.Errorf("%q survived in the events", v)
		}
	}
	if !bytes.Contains(got, []byte("PD 08(e0xfc/s1) Path ")) || !bytes.Contains(got, []byte(" reset (Type 03)")) {
		t.Error("the surrounding text must stay")
	}
	if binary.LittleEndian.Uint32(got[8:]) != 500 || binary.LittleEndian.Uint32(got[0:]) != 3 {
		t.Error("counts and sequence numbers must stay")
	}
}

func TestScrubEventWithAHugeCountDoesNotPanic(t *testing.T) {
	raw := eventListBuf(1, eventTexts())
	binary.LittleEndian.PutUint32(raw[0:], 0xFFFFFFFF)

	if got := testScrubber().scrub(kindEvents, raw); len(got) != len(raw) {
		t.Fatal("length changed")
	}
}

func TestScrubLearnsDriveSerialsFromTheDriveInfoAndFindsThemInEvents(t *testing.T) {
	s := testScrubber()
	s.scrub(kindPDInfo, pdInfoBuf(9, rawDriveSN0))

	got := s.scrub(kindEvents, eventListBuf(1, eventTexts()))

	if bytes.Contains(got, []byte(rawDriveSN0)) {
		t.Fatal("the drive serial survived in an event")
	}
}

func TestScrubLeavesOtherKindsAlone(t *testing.T) {
	raw := []byte("battery status and other numbers")

	if got := testScrubber().scrub("bbu_get_status", raw); !bytes.Equal(got, raw) {
		t.Fatal("an unlisted kind must pass through")
	}
}

func TestVerifyPassesAScrubbedBufferAndFailsARawOne(t *testing.T) {
	s := testScrubber()
	cases := map[string][]byte{
		kindCtrlInfo: ctrlInfoBuf(rawCtrlSerial, true),
		kindPDList:   pdListBuf([]uint16{0, 8}),
		kindPDInfo:   pdInfoBuf(0, rawDriveSN0),
		kindLDInfo:   ldInfoBuf(rawLDName),
		kindLDProps:  ldPropsBuf(rawLDName),
		kindCfgRead:  cfgReadBuf(rawLDName),
		kindEvents:   eventListBuf(1, eventTexts()),
	}
	for kind, raw := range cases {
		if problems := verifyBuffer(kind, s.scrub(kind, raw)); len(problems) != 0 {
			t.Errorf("%s scrubbed: %v", kind, problems)
		}
		if problems := verifyBuffer(kind, raw); len(problems) == 0 {
			t.Errorf("%s raw: verify found nothing", kind)
		}
	}
}

func TestVerifyFindsASerialAMacAWwnAndAHostnameLikeNameInAnyBuffer(t *testing.T) {
	for name, text := range map[string]string{"mac": rawMAC, "wwn": rawWWN} {
		raw := append([]byte("some other buffer "), text...)
		if problems := verifyBuffer("bbu_get_status", raw); len(problems) == 0 {
			t.Errorf("%s in an unlisted buffer was not found", name)
		}
	}
	if problems := verifyBuffer("bbu_get_status", []byte("voltage 4096 charge 87")); len(problems) != 0 {
		t.Errorf("a clean buffer was flagged: %v", problems)
	}
}

func eventsWith(texts ...string) []byte { return eventListBuf(7, texts) }

func TestScrubEventsReplacesSerialShapedTokensInEveryPosition(t *testing.T) {
	serials := []string{"S3Z9NB0K123456", "Zx4Q9m2LpW7aR1tY5uK8", "WD-WCC4N0XYZ123", "12345678901", "abcd1234efgh"}
	var texts []string
	for _, sn := range serials {
		texts = append(texts, "Removed PD 09 SN "+sn, sn+" failed", "PD 03 "+sn+" reset (Type 03)")
	}
	raw := eventsWith(texts...)
	for i := range texts {
		clear(raw[8+256*i+32 : 8+256*i+128])
	}

	if p := verifyBuffer(kindEvents, raw); len(p) < len(texts) {
		t.Fatalf("raw events: %d findings for %d texts", len(p), len(texts))
	}
	got := testScrubber().scrub(kindEvents, raw)

	if len(got) != len(raw) {
		t.Fatal("length changed")
	}
	for _, sn := range serials {
		for _, part := range strings.Split(sn, "-") {
			if len(part) >= 8 && bytes.Contains(got, []byte(part)) {
				t.Errorf("a %d-char serial part survived", len(part))
			}
		}
	}
	if p := verifyBuffer(kindEvents, got); len(p) != 0 {
		t.Errorf("scrubbed events: %v", p)
	}
	if !bytes.Contains(got, []byte("Removed PD 09 SN SCRUB")) || !bytes.Contains(got, []byte(" reset (Type 03)")) {
		t.Error("the surrounding text must stay")
	}
}

func TestScrubEventsReplacesNAAAndSeparatedWWN(t *testing.T) {
	naa := "6" + "0123456789abcdef0123456789abcde"
	wwn := "50:00:c5:00:a1:b2:c3:d4"
	raw := eventsWith("Volume "+naa+" online", "Path "+wwn+" up", "Path 5000c500a1b2c3d4 up")

	if p := verifyBuffer(kindEvents, raw); len(p) < 3 {
		t.Fatalf("raw findings = %v", p)
	}
	got := testScrubber().scrub(kindEvents, raw)

	for _, v := range []string{naa, wwn, "5000c500a1b2c3d4"} {
		if bytes.Contains(got, []byte(v)) {
			t.Errorf("a %d-char id survived", len(v))
		}
	}
	if p := verifyBuffer(kindEvents, got); len(p) != 0 {
		t.Errorf("scrubbed events: %v", p)
	}
}

func TestScrubEventsKeepsBenignText(t *testing.T) {
	texts := []string{
		"PD 08(e0xfc/s1) Path reset (Type 03)",
		"Patrol read 12345 done at seq 4096, 1700000000 s",
		"State change on PD 04(e0xfc/s2) 0x0000001f to 0x00000002",
		"Controller ID: 0 Unexpected sense: Initialization Uncorrectable",
		"Time 2026-11-16 10:20:30 firmware 23.34.0-0017 deadbeef",
		"CONSISTENCY CHECK started on VD 02",
	}
	raw := eventsWith(texts...)

	got := testScrubber().scrub(kindEvents, raw)

	for i, text := range texts {
		e := got[8+256*i+128 : 8+256*i+256]
		if !strings.HasPrefix(string(e), text+"\x00") {
			t.Errorf("text %d changed", i)
		}
	}
	if p := verifyBuffer(kindEvents, got); len(p) != 0 {
		t.Errorf("benign text flagged: %v", p)
	}
}

func TestSerialShapedRule(t *testing.T) {
	cases := map[string]bool{
		"S3Z9NB0K123456": true, "ZA1B2C3D": true, "12345678901": true, "Abcdefg1": true,
		"12345678": false, "1234567890": false, "ZA1B2C3": false, "Controller": false,
		"0x0000001f": false, "0x1234567890": true, "deadbeef": false, "SCRUBGHJ1": false,
	}
	for tok, want := range cases {
		if got := serialShaped([]byte(tok)); got != want {
			t.Errorf("serialShaped(%d chars, digits %v) = %v", len(tok), strings.ContainsAny(tok, "0123456789"), got)
		}
	}
}

func TestHighEntropyRule(t *testing.T) {
	if !highEntropy([]byte("kJqzXwPvBmRt")) {
		t.Error("a random mixed-case run passed")
	}
	for _, w := range []string{"Controller", "Initialization", "Uncorrectable", "CONSISTENCY"} {
		if highEntropy([]byte(w)) {
			t.Errorf("the word %q was flagged", w)
		}
	}
	if highEntropy([]byte("SCRUBkJqzXwPvBmRt")) {
		t.Error("a scrub token was flagged")
	}
}

func TestScrubLDInfoReplacesTheVolumeIdAndKeepsTheRest(t *testing.T) {
	raw := ldInfoBuf(rawLDName)

	got := testScrubber().scrub(kindLDInfo, raw)

	if len(got) != len(raw) || bytes.Contains(got, rawNAA) {
		t.Fatal("length changed or the volume id survived")
	}
	if !bytes.Equal(got[304:312], raw[304:312]) || !bytes.HasPrefix(got[312:], []byte("SCRUB")) || !allZero(got[328:]) {
		t.Errorf("designator header or id wrong")
	}
	if p := verifyBuffer(kindLDInfo, got); len(p) != 0 {
		t.Errorf("scrubbed: %v", p)
	}
	p := verifyBuffer(kindLDInfo, raw)
	var found bool
	for _, m := range p {
		found = found || strings.Contains(m, "NAA-6") && strings.Contains(m, "offset 312")
	}
	if !found {
		t.Errorf("raw findings = %v", p)
	}
}

func TestVerifyFlagsAsciiNAAInAnyBuffer(t *testing.T) {
	raw := []byte("vol 6" + "0123456789abcdef0123456789abcde end")
	if p := verifyBuffer("bbu_get_status", raw); len(p) == 0 {
		t.Error("an ASCII NAA-6 id passed")
	}
	for _, m := range verifyBuffer("bbu_get_status", raw) {
		if strings.Contains(m, "0123456789") {
			t.Error("a finding prints the value")
		}
	}
}

func TestScrubAndVerifyShortBuffersDoNotPanic(t *testing.T) {
	s := testScrubber()
	for _, kind := range []string{kindLDInfo, kindLDProps, kindEvents, kindCfgRead, kindPDInfo} {
		for n := 0; n < 400; n += 7 {
			raw := make([]byte, n)
			copy(raw[min(n, 304):], []byte{0, 0x83, 0xff, 0xff, 1, 3, 0, 255, 'x', 'y'})
			verifyBuffer(kind, s.scrub(kind, raw))
			verifyBuffer(kind, raw)
		}
	}
}
