// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every text sample file starts with one "# provenance:" line that says whether its lines are real
// (captured, with names and addresses replaced), invented (written from the format description) or a
// mix: "partly real; real lines: 1, 3-4", where the numbers count the sample lines, not the comments.

func TestSampleProvenance_EveryFileCarriesOneValidMarker(t *testing.T) {
	entries, err := os.ReadDir(samplesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("no sample files in %s", samplesDir)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(samplesDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseSampleFile(raw); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
}

func TestSampleProvenance_TheMarkerIsRefusedWhenItIsWrong(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string // part of the error
	}{
		{"no marker", "line one\nline two\n", "no # provenance: line"},
		{"two markers", "# provenance: real\n# provenance: invented\nline\n", "more than one"},
		{"unknown value", "# provenance: genuine\nline\n", "unknown value"},
		{"empty value", "# provenance:\nline\n", "unknown value"},
		{"partly real without a list", "# provenance: partly real\nline one\nline two\n", "real lines"},
		{"partly real with an empty list", "# provenance: partly real; real lines:\nline one\nline two\n", "real lines"},
		{"list is not numbers", "# provenance: partly real; real lines: one\nline one\nline two\n", "not a line number"},
		{"list beyond the file", "# provenance: partly real; real lines: 3\nline one\nline two\n", "beyond"},
		{"list starts at zero", "# provenance: partly real; real lines: 0\nline one\nline two\n", "beyond"},
		{"backwards range", "# provenance: partly real; real lines: 2-1\nline one\nline two\n", "backwards"},
		{"every line listed", "# provenance: partly real; real lines: 1-2\nline one\nline two\n", "every line"},
		{"real with a list", "# provenance: real; real lines: 1\nline one\n", "only for partly real"},
		{"invented with a list", "# provenance: invented; real lines: 1\nline one\n", "only for partly real"},
		{"no sample line", "# provenance: real\n", "no sample line"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSampleFile([]byte(tc.file))
			if err == nil {
				t.Fatalf("want an error holding %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not hold %q", err, tc.want)
			}
		})
	}
}

func TestSampleProvenance_TheMarkerIsAcceptedWhenItIsRight(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		value     string
		realLines []int
		lines     int
	}{
		{"real", "# provenance: real\none\ntwo\n", "real", nil, 2},
		{"invented", "# provenance: invented\none\n", "invented", nil, 1},
		{"partly real", "# provenance: partly real; real lines: 1, 3-4\none\ntwo\nthree\nfour\nfive\n", "partly real", []int{1, 3, 4}, 5},
		{"marker after a note and blank lines", "# note\n\n# provenance: invented\none\n", "invented", nil, 1},
		{"a leading space belongs to a line", "# provenance: invented\n one\n", "invented", nil, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSampleFile([]byte(tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got.provenance != tc.value {
				t.Errorf("provenance = %q, want %q", got.provenance, tc.value)
			}
			if len(got.lines) != tc.lines {
				t.Errorf("%d sample lines, want %d: the marker and comment lines are not records", len(got.lines), tc.lines)
			}
			if !slices.Equal(got.realLines, tc.realLines) {
				t.Errorf("real lines = %v, want %v", got.realLines, tc.realLines)
			}
		})
	}
}

func TestSampleProvenance_TheMarkerLinesPassTheScrubCheckToo(t *testing.T) {
	entries, err := os.ReadDir(samplesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(samplesDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "#") {
				continue
			}
			for what, re := range scrub {
				if re.MatchString(line) {
					t.Errorf("%s line %d holds a %s: %q", e.Name(), n+1, what, line)
				}
			}
		}
	}
}

func TestSampleProvenance_TheCountsAreWhatTheMarkersSay(t *testing.T) {
	files := loadSampleFiles(t)
	counts := map[string]int{}
	for _, f := range files {
		counts[f.provenance]++
	}
	if len(files) != counts["real"]+counts["partly real"]+counts["invented"] {
		t.Errorf("%d files, counts %v", len(files), counts)
	}
	t.Logf("%d sample files: %d real, %d partly real, %d invented",
		len(files), counts["real"], counts["partly real"], counts["invented"])
}
