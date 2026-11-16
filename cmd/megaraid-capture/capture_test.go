// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeController answers every allowed opcode with a fixture, like a 3108 with a CacheVault.
type fakeController struct {
	bbu      bool
	pds      []uint16
	lds      []byte
	newest   uint32
	oldest   uint32
	status   map[uint32]uint8 // a non-zero status for an opcode
	failWith map[uint32]error // an error for an opcode
	seen     []request
}

func newFakeController() *fakeController {
	return &fakeController{bbu: true, pds: []uint16{4, 9}, lds: []byte{0}, newest: 500, oldest: 1}
}

func fit(b []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, b)
	return out
}

func (f *fakeController) exec(req request) (response, error) {
	f.seen = append(f.seen, req)
	if err := f.failWith[req.opcode]; err != nil {
		return response{}, err
	}
	if st := f.status[req.opcode]; st != 0 {
		return response{status: st, data: make([]byte, req.dataLen)}, nil
	}
	var data []byte
	switch req.opcode {
	case opCtrlGetInfo:
		data = ctrlInfoBuf(rawCtrlSerial, f.bbu)
	case opPDGetList, opPDListQuery:
		data = pdListBuf(f.pds)
	case opPDGetInfo:
		id := binary.LittleEndian.Uint16(req.mbox[:2])
		serial := rawDriveSN0
		if id == 9 {
			serial = rawDriveSN1
		}
		data = pdInfoBuf(id, serial)
	case opLDGetList, opLDListQuery:
		data = ldListBuf(f.lds)
	case opLDGetInfo:
		data = ldInfoBuf(rawLDName)
	case opLDGetProp:
		data = ldPropsBuf(rawLDName)
	case opCfgRead:
		data = cfgReadBuf(rawLDName)
	case opBBUGetDesignInfo:
		data = bbuDesignBuf()
	case opEventGetInfo:
		data = eventInfoBuf(f.newest, f.oldest)
	case opEventGet:
		data = eventListBuf(binary.LittleEndian.Uint32(req.mbox[:4]), eventTexts())
	default:
		data = bytes.Repeat([]byte{0x11}, 16)
	}
	return response{data: fit(data, req.dataLen)}, nil
}

func (f *fakeController) opcodes() map[uint32]int {
	out := map[uint32]int{}
	for _, r := range f.seen {
		out[r.opcode]++
	}
	return out
}

func runCapture(t *testing.T, f *fakeController, opt captureOptions) *bundle {
	t.Helper()
	if opt.scrub == nil {
		opt.scrub = testScrubber()
	}
	if opt.events == 0 {
		opt.events = 100
	}
	b, err := captureController(f, 3, opt)
	if err != nil {
		t.Fatalf("captureController: %v", err)
	}
	return b
}

func TestCaptureSendsOnlyAllowedOpcodesAndEachOneAsExpected(t *testing.T) {
	f := newFakeController()

	runCapture(t, f, captureOptions{label: "lab"})

	got := f.opcodes()
	want := map[uint32]int{
		opCtrlGetInfo: 1, opCtrlGetProps: 1, opPRGetStatus: 1, opPRGetProperties: 1,
		opPDGetList: 1, opPDListQuery: 1, opPDGetInfo: 2, opLDGetList: 1, opLDListQuery: 1,
		opLDGetInfo: 1, opLDGetProp: 1, opCfgRead: 1,
		opBBUGetStatus: 1, opBBUGetCapacityInfo: 1, opBBUGetDesignInfo: 1, opBBUGetProp: 1,
		opEventGetInfo: 1, opEventGet: 1,
	}
	for op, n := range want {
		if got[op] != n {
			t.Errorf("opcode %#x sent %d times, want %d", op, got[op], n)
		}
	}
	for op := range got {
		if _, ok := allowed[op]; !ok {
			t.Errorf("opcode %#x is not allowed", op)
		}
	}
	if f.seen[0].opcode != opCtrlGetInfo {
		t.Error("the controller info must come first")
	}
}

func TestCaptureOfAControllerWithoutABatterySendsNoBatteryCommand(t *testing.T) {
	f := newFakeController()
	f.bbu = false

	b := runCapture(t, f, captureOptions{label: "lab"})

	for _, op := range []uint32{opBBUGetStatus, opBBUGetCapacityInfo, opBBUGetDesignInfo, opBBUGetProp} {
		if f.opcodes()[op] != 0 {
			t.Errorf("battery opcode %#x was sent", op)
		}
	}
	found := false
	for _, s := range b.manifest.Skipped {
		if strings.Contains(s.Name, "bbu") && strings.Contains(s.Reason, "no battery") {
			found = true
		}
	}
	if !found {
		t.Errorf("the skipped battery is not in the manifest: %+v", b.manifest.Skipped)
	}
}

func TestCaptureReadsDriveInfoByDeviceIdWithTheIdInTheMailbox(t *testing.T) {
	f := newFakeController()

	b := runCapture(t, f, captureOptions{label: "lab"})

	var ids []uint16
	for _, r := range f.seen {
		if r.opcode == opPDGetInfo {
			ids = append(ids, binary.LittleEndian.Uint16(r.mbox[:2]))
			if r.dataLen != 512 {
				t.Errorf("PD_GET_INFO buffer = %d, want 512", r.dataLen)
			}
		}
	}
	if len(ids) != 2 || ids[0] != 4 || ids[1] != 9 {
		t.Fatalf("device ids = %v, want [4 9]", ids)
	}
	if _, ok := b.files["pd_get_info_4.bin"]; !ok {
		t.Errorf("files = %v", fileNames(b))
	}
}

func fileNames(b *bundle) []string {
	var names []string
	for n := range b.files {
		names = append(names, n)
	}
	return names
}

func TestCaptureReadsEachLogicalDriveByTargetId(t *testing.T) {
	f := newFakeController()
	f.lds = []byte{0, 2}

	b := runCapture(t, f, captureOptions{label: "lab"})

	for _, name := range []string{"ld_get_info_0.bin", "ld_get_info_2.bin", "ld_get_prop_0.bin", "ld_get_prop_2.bin"} {
		if _, ok := b.files[name]; !ok {
			t.Errorf("%s is missing from %v", name, fileNames(b))
		}
	}
}

func TestCaptureReadsTheNewestEventsWithinTheLimit(t *testing.T) {
	f := newFakeController()

	runCapture(t, f, captureOptions{label: "lab", events: 100})

	var get request
	for _, r := range f.seen {
		if r.opcode == opEventGet {
			get = r
		}
	}
	if seq := binary.LittleEndian.Uint32(get.mbox[:4]); seq != 401 {
		t.Errorf("first sequence = %d, want 401 (newest 500, 100 events)", seq)
	}
	if word := binary.LittleEndian.Uint32(get.mbox[4:8]); word != 0xFE00FFFF {
		t.Errorf("class and locale word = %#x, want 0xFE00FFFF (debug and up, every locale)", word)
	}
	if get.dataLen != 8+256*100 {
		t.Errorf("event buffer = %d, want %d", get.dataLen, 8+256*100)
	}
}

func TestCaptureStartsAtTheOldestEventWhenTheLogIsShort(t *testing.T) {
	f := newFakeController()
	f.newest, f.oldest = 30, 12

	runCapture(t, f, captureOptions{label: "lab", events: 100})

	for _, r := range f.seen {
		if r.opcode == opEventGet && binary.LittleEndian.Uint32(r.mbox[:4]) != 12 {
			t.Errorf("first sequence = %d, want 12", binary.LittleEndian.Uint32(r.mbox[:4]))
		}
	}
}

func TestCaptureWithZeroEventsReadsOnlyTheEventState(t *testing.T) {
	f := newFakeController()

	runCapture(t, f, captureOptions{label: "lab", events: -1})

	if f.opcodes()[opEventGet] != 0 || f.opcodes()[opEventGetInfo] != 1 {
		t.Fatalf("event commands = %v", f.opcodes())
	}
}

func TestCaptureRecordsANonZeroStatusAndGoesOn(t *testing.T) {
	f := newFakeController()
	f.status = map[uint32]uint8{opPDListQuery: 2, opLDListQuery: 2}

	b := runCapture(t, f, captureOptions{label: "lab"})

	if _, ok := b.files["pd_list_query.bin"]; ok {
		t.Error("a rejected command must not leave a file")
	}
	var entry *manifestEntry
	for i := range b.manifest.Commands {
		if b.manifest.Commands[i].Name == "pd_list_query" {
			entry = &b.manifest.Commands[i]
		}
	}
	if entry == nil || entry.Status != 2 || entry.File != "" {
		t.Fatalf("manifest entry = %+v", entry)
	}
	if _, ok := b.files["ctrl_getinfo.bin"]; !ok || f.opcodes()[opEventGet] != 1 {
		t.Error("the capture must continue after a rejected command")
	}
}

func TestCaptureStopsOnAFatalErrorAndReturnsNoBundle(t *testing.T) {
	for _, fatal := range []error{errNeedsCapability, errControllerGone, errControllerBusy, errTimeout, errDeviceBroken} {
		f := newFakeController()
		f.failWith = map[uint32]error{opPDGetList: fatal}

		b, err := captureController(f, 3, captureOptions{label: "lab", scrub: testScrubber(), events: 10})

		if !errors.Is(err, fatal) || b != nil {
			t.Errorf("%v: bundle=%v err=%v", fatal, b, err)
		}
		if f.opcodes()[opEventGet] != 0 {
			t.Errorf("%v: the capture went on after the error", fatal)
		}
	}
}

func TestCaptureBundleHoldsNoRawIdentity(t *testing.T) {
	f := newFakeController()
	b := runCapture(t, f, captureOptions{label: "lab", events: 3})

	manifest, err := json.Marshal(b.manifest)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string][]byte{"manifest": manifest}
	for n, data := range b.files {
		all[n] = data
	}
	for name, data := range all {
		for _, v := range sensitiveValues() {
			if bytes.Contains(data, v) {
				t.Errorf("%s holds %q", name, v)
			}
		}
		if problems := verifyBuffer(kindOf(name), data); len(problems) != 0 {
			t.Errorf("%s: %v", name, problems)
		}
	}
}

func TestCaptureManifestDescribesTheRun(t *testing.T) {
	b := runCapture(t, newFakeController(), captureOptions{label: "lab"})

	m := b.manifest
	if m.Tool != "megaraid-capture" || m.Version != version || m.Label != "lab" || m.HostNo != 3 || !m.Scrubbed {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Commands) < 10 {
		t.Fatalf("commands = %d", len(m.Commands))
	}
	for _, c := range m.Commands {
		if !strings.HasPrefix(c.Opcode, "0x") || c.Name == "" {
			t.Errorf("entry %+v", c)
		}
		if c.File != "" {
			if _, ok := b.files[c.File]; !ok {
				t.Errorf("entry %s names a missing file %s", c.Name, c.File)
			}
		}
	}
}

func TestWriteBundleCreatesFilesAndManifest(t *testing.T) {
	b := runCapture(t, newFakeController(), captureOptions{label: "lab"})
	root := t.TempDir()

	dir, err := writeBundle(filepath.Join(root, "out"), b)

	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "lab-host3" {
		t.Errorf("dir = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Error(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ctrl_getinfo.bin"))
	if err != nil || len(data) != 2048 {
		t.Errorf("ctrl_getinfo.bin: %d bytes, %v", len(data), err)
	}
	if problems := verifyDir(dir); len(problems) != 0 {
		t.Errorf("verifyDir: %v", problems)
	}
}

func TestVerifyDirFindsARawFile(t *testing.T) {
	b := runCapture(t, newFakeController(), captureOptions{label: "lab"})
	dir, err := writeBundle(filepath.Join(t.TempDir(), "out"), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ctrl_getinfo.bin"), ctrlInfoBuf(rawCtrlSerial, true), 0o600); err != nil {
		t.Fatal(err)
	}

	problems := verifyDir(dir)

	if len(problems) == 0 {
		t.Fatal("a raw controller info file passed the check")
	}
}

func TestWriteBundleRefusesAnExistingNonEmptyDirectory(t *testing.T) {
	b := runCapture(t, newFakeController(), captureOptions{label: "lab"})
	root := filepath.Join(t.TempDir(), "out")
	if _, err := writeBundle(root, b); err != nil {
		t.Fatal(err)
	}

	if _, err := writeBundle(root, b); err == nil {
		t.Fatal("a second write over the same bundle was accepted")
	}
}

func TestWriteBundleFilesAreNotWorldReadable(t *testing.T) {
	b := runCapture(t, newFakeController(), captureOptions{label: "lab"})
	dir, err := writeBundle(filepath.Join(t.TempDir(), "out"), b)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "ctrl_getinfo.bin"))

	if err != nil || info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("mode = %v, err = %v", info.Mode(), err)
	}
}
