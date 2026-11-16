// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const leftoverSerial = "QZ9X8W7V6U5T" // invented; the old rules left this in event text

// oldBundle writes a bundle as an earlier scrubber would have left it: scrubbed, except a
// serial-like token in event text and the volume id of the logical drive.
func oldBundle(t *testing.T) (dir string, eventTokenAt int) {
	t.Helper()
	sc := newScrubber([]byte("old-salt"), []string{rawHostname})
	files := map[string][]byte{
		"ctrl_getinfo.bin":        sc.scrub(kindCtrlInfo, ctrlInfoBuf(rawCtrlSerial, true)),
		"pd_get_list.bin":         sc.scrub(kindPDList, pdListBuf([]uint16{8, 9})),
		"pd_get_info_8.bin":       sc.scrub(kindPDInfo, pdInfoBuf(8, rawDriveSN0)),
		"cfg_read.bin":            sc.scrub(kindCfgRead, cfgReadBuf(rawLDName)),
		"bbu_get_design_info.bin": sc.scrub(kindBBUDesign, bbuDesignBuf()),
		"ld_get_prop_0.bin":       sc.scrub(kindLDProps, ldPropsBuf(rawLDName)),
		"ctrl_get_props.bin":      {1, 2, 3, 4, 5, 6, 7, 8},
	}
	ld := sc.scrub(kindLDInfo, ldInfoBuf(rawLDName))
	copy(ld[312:], rawNAA)
	files["ld_get_info_0.bin"] = ld

	ev := sc.scrub(kindEvents, eventListBuf(10, eventTexts()))
	desc := ev[8+128 : 8+128+128]
	clear(desc)
	copy(desc, "Removed: PD 09 SN "+leftoverSerial+" now")
	files["event_get.bin"] = ev
	eventTokenAt = 8 + 128 + strings.Index(string(desc), leftoverSerial)

	if problems := verifyDir2(files); len(problems) == 0 {
		t.Fatal("the old bundle must fail verify")
	}
	root := t.TempDir()
	dir, err := writeBundle(root, &bundle{files: files, manifest: manifest{Tool: "megaraid-capture", Label: "old", Scrubbed: true}})
	if err != nil {
		t.Fatal(err)
	}
	return dir, eventTokenAt
}

func verifyDir2(files map[string][]byte) []string {
	var out []string
	for name, data := range files {
		out = append(out, verifyBuffer(kindOf(name), data)...)
	}
	return out
}

func readBundle(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got[e.Name()] = b
	}
	return got
}

func rescrubRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runRescrub(args, bytes.NewReader(bytes.Repeat([]byte{7}, 32)), &stdout, &stderr)
	return code, stderr.String()
}

func TestRescrubKeepsSizesAndChangesOnlyTheLeftovers(t *testing.T) {
	in, tokenAt := oldBundle(t)
	out := filepath.Join(t.TempDir(), "out")

	if code, msg := rescrubRun(t, "-salt", "new-salt", in, out); code != 0 {
		t.Fatalf("exit = %d: %s", code, msg)
	}

	before, after := readBundle(t, in), readBundle(t, out)
	if len(before) != len(after) {
		t.Fatalf("file count %d became %d", len(before), len(after))
	}
	for name, b := range before {
		a, ok := after[name]
		if !ok || len(a) != len(b) && name != "manifest.json" {
			t.Fatalf("%s: missing or size changed", name)
		}
		if name == "manifest.json" { // only the note is added
			if !bytes.Contains(a, []byte(`"rescrubbed": true`)) {
				t.Error("manifest lacks the rescrubbed note")
			}
			continue
		}
		var lo, hi int // allowed range of differences
		switch name {
		case "event_get.bin":
			lo, hi = tokenAt, tokenAt+len(leftoverSerial)
		case "ld_get_info_0.bin":
			lo, hi = 312, 312+len(rawNAA)
		}
		for i := range b {
			if a[i] != b[i] && (i < lo || i >= hi) {
				t.Fatalf("%s: byte %d changed outside the scrubbed range", name, i)
			}
		}
		if (lo != hi) != !bytes.Equal(a, b) {
			t.Errorf("%s: changed = %v, want %v", name, !bytes.Equal(a, b), lo != hi)
		}
	}
	if bytes.Contains(after["event_get.bin"], []byte(leftoverSerial)) || bytes.Contains(after["ld_get_info_0.bin"], rawNAA) {
		t.Error("a leftover survived")
	}
	if p := verifyDir(out); len(p) != 0 {
		t.Errorf("verify: %v", p)
	}
	fi, err := os.Stat(filepath.Join(out, "event_get.bin"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, %v", fi.Mode(), err)
	}
	if di, err := os.Stat(out); err != nil || di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, %v", di.Mode(), err)
	}
}

func TestRescrubTwiceWithOneSaltIsIdentical(t *testing.T) {
	in, _ := oldBundle(t)
	tmp := t.TempDir()
	out1, out2 := filepath.Join(tmp, "o1"), filepath.Join(tmp, "o2")

	if code, msg := rescrubRun(t, "-salt", "s", in, out1); code != 0 {
		t.Fatalf("first: %d %s", code, msg)
	}
	if code, msg := rescrubRun(t, "-salt", "s", out1, out2); code != 0 {
		t.Fatalf("second: %d %s", code, msg)
	}

	a, b := readBundle(t, out1), readBundle(t, out2)
	for name, data := range a {
		if !bytes.Equal(data, b[name]) {
			t.Errorf("%s differs on the second pass", name)
		}
	}
}

func TestRescrubUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{}, {"only-one"}, {"a", "b", "c"}, {"-nope", "a", "b"}} {
		if code, _ := rescrubRun(t, args...); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
	}
}

func TestRescrubRefusesAnExistingOut(t *testing.T) {
	in, _ := oldBundle(t)
	out := t.TempDir()

	code, msg := rescrubRun(t, in, out)

	if code != 1 || !strings.Contains(msg, "exists") {
		t.Errorf("exit = %d, %q", code, msg)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Error("an existing OUT must stay untouched")
	}
}

func TestRescrubFailsWhenVerifyStillFinds(t *testing.T) {
	in, _ := oldBundle(t)
	// A WWN in a file of no known kind is not rescrubbed, so verify keeps failing on it.
	if err := os.WriteFile(filepath.Join(in, "ctrl_get_props.bin"), []byte(rawWWN), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")

	code, msg := rescrubRun(t, in, out)

	if code != 1 || !strings.Contains(msg, "failed") {
		t.Errorf("exit = %d, %q", code, msg)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("OUT is kept after a failed verify")
	}
}

func TestRescrubRejectsAnInputWithoutManifest(t *testing.T) {
	in := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")

	if code, _ := rescrubRun(t, in, out); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestRunDispatchesRescrub(t *testing.T) {
	te := newTestEnv(t, nil)

	if code := te.run("rescrub"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
