// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files from the current output")

const goldenDir = "testdata/golden"

// goldenRecord is what a sample line becomes. The time of the record is left out: some parsers read a
// line that has no year and take the year of the clock.
type goldenRecord struct {
	Body         string         `json:"body"`
	Severity     int32          `json:"severity"`
	SeverityText string         `json:"severity_text"`
	Scope        string         `json:"scope"`
	Resource     map[string]any `json:"resource"`
	Attributes   map[string]any `json:"attributes"`
}

func goldenOf(t *testing.T, lines []string) string {
	t.Helper()
	var records []goldenRecord
	for _, r := range flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))) {
		records = append(records, goldenRecord{
			Body: r.body, Severity: int32(r.severity), SeverityText: r.severityText,
			Scope: r.scope, Resource: r.resource, Attributes: r.attrs,
		})
	}
	text, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(text) + "\n"
}

func checkGolden(t *testing.T, name string, lines []string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".json")
	got := goldenOf(t, lines)
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (go test -run TestGolden -update writes it)", path, err)
	}
	if got != string(want) {
		t.Errorf("the records of %s differ from %s; run go test -run TestGolden -update and read the diff:\n%s", name, path, firstDifference(string(want), got))
	}
}

// firstDifference shows the first line where the two texts differ.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range min(len(w), len(g)) {
		if w[i] != g[i] {
			return fmt.Sprintf("line %d\nwant: %s\ngot:  %s", i+1, w[i], g[i])
		}
	}
	return fmt.Sprintf("the texts have %d and %d lines", len(w), len(g))
}

func TestGolden_EverySampleFileGivesTheRecordsItGaveBefore(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) { checkGolden(t, file, lines) })
	}
}

// Every form of a syslog wrapper, with the inner lines that get their own branch.
func TestGolden_EveryFormOfSyslogLineGivesTheRecordItGaveBefore(t *testing.T) {
	checkGolden(t, "zz-syslog-forms", []string{
		"Dec 31 10:05:00 alpha sshd[1]: one",
		"2026-11-20T00:03:18.148343+01:00 nas sshd[12]: Accepted publickey for tyler",
		"1 2026-11-20T00:03:18.148343+01:00 nas smartd 1517 - - Device: /dev/sda [SAT], 2 Currently unreadable (pending) sectors",
		"2026-11-20T00:03:18.148343+01:00 docker-host amazon-ssm-agent[1517]: 2026-11-20 00:03:18.1234 WARN [ssm-agent-worker] Failed to connect to Systems Manager",
		"123: *Dec 31 10:05:00.123: %LINK-3-UPDOWN: Interface Gi0/1, changed state to down",
		"  Dec 31 10:05:00 alpha sshd[2]: padded wrapper  ",
	})
}

// The samples of every format in one batch, in the order of the file names, so records of different
// formats follow each other as they do in a real batch.
func TestGolden_AMixedBatchGivesTheRecordsItGaveBefore(t *testing.T) {
	samples := loadSamples(t)
	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	slices.Sort(names)
	var mixed []string
	for _, name := range names {
		mixed = append(mixed, samples[name]...)
	}

	checkGolden(t, "zz-mixed", mixed)
}
