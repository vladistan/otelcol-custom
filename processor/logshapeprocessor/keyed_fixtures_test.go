// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore awselb eksaudit gcplb gcpaudit serialconsole stderrhint s3access

package logshapeprocessor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/rename"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// keyedSamplesDir holds one JSON-lines file per branch that claims by attributes. A text sample file
// holds bare lines, so these branches cannot be reached from testdata/samples.
const keyedSamplesDir = "testdata/samples-keyed"

// keyedSample is one record as the receiver hands it over: body, record attributes, resource attributes
// and the stored severity number. Provenance is "real" for a line or record layout quoted from a capture
// (names and addresses replaced) and "invented" for one written from the format description.
type keyedSample struct {
	Provenance     string         `json:"provenance"`
	Body           string         `json:"body"`
	Attributes     map[string]any `json:"attributes"`
	Resource       map[string]any `json:"resource"`
	SeverityNumber int32          `json:"severity_number"`
	line           string
}

// keyedBranches are the branches whose samples live in keyedSamplesDir.
func keyedBranches() []string {
	out := append([]string{"flow"}, sourceKeyedBranches...)
	for name := range attributeKeyed {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// jsonNumbers turns json.Number values into int64 when they are whole numbers and float64 otherwise,
// so that a status code stays an integer attribute.
func jsonNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil && !strings.ContainsAny(x.String(), ".eE") {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = jsonNumbers(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = jsonNumbers(e)
		}
		return x
	}
	return v
}

func loadKeyedSamples(tb testing.TB) map[string][]keyedSample {
	tb.Helper()
	entries, err := os.ReadDir(keyedSamplesDir)
	if err != nil {
		tb.Fatalf("read %s: %v", keyedSamplesDir, err)
	}
	out := map[string][]keyedSample{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".jsonl")
		raw, err := os.ReadFile(filepath.Join(keyedSamplesDir, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
				continue // blank, or a comment line: the file header carries the provenance note and the spell-check words
			}
			var s keyedSample
			dec := json.NewDecoder(bytes.NewReader([]byte(line)))
			dec.UseNumber()
			dec.DisallowUnknownFields()
			if err := dec.Decode(&s); err != nil {
				tb.Fatalf("%s line %d: %v", e.Name(), n+1, err)
			}
			s.line = line
			s.Attributes, _ = jsonNumbers(s.Attributes).(map[string]any)
			s.Resource, _ = jsonNumbers(s.Resource).(map[string]any)
			out[name] = append(out[name], s)
		}
	}
	return out
}

// keyedBatch is one record in its own resource, so a record never shares scope or resource with another.
func keyedBatch(s keyedSample) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if err := rl.Resource().Attributes().FromRaw(s.Resource); err != nil {
		panic(err)
	}
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr(s.Body)
	if err := lr.Attributes().FromRaw(s.Attributes); err != nil {
		panic(err)
	}
	lr.SetSeverityNumber(plog.SeverityNumber(s.SeverityNumber))
	return ld
}

func keyedOnly(name string) *Config {
	cfg := defaultConfig()
	cfg.Branches = []string{name}
	return cfg
}

func TestKeyedFixtures_EveryKeyedBranchHasASampleFile(t *testing.T) {
	samples := loadKeyedSamples(t)
	for _, name := range keyedBranches() {
		if len(samples[name]) < 2 {
			t.Errorf("branch %s has %d records in %s, want at least 2", name, len(samples[name]), keyedSamplesDir)
		}
	}
	for file := range samples {
		if !slices.Contains(keyedBranches(), file) {
			t.Errorf("%s/%s.jsonl names no attribute-keyed branch", keyedSamplesDir, file)
		}
	}
}

func TestKeyedFixtures_EveryRecordSaysIfItIsRealOrInvented(t *testing.T) {
	for file, records := range loadKeyedSamples(t) {
		for i, s := range records {
			if s.Provenance != "real" && s.Provenance != "invented" {
				t.Errorf("%s.jsonl record %d: provenance = %q, want real or invented", file, i+1, s.Provenance)
			}
		}
	}
}

// provenanceHeader starts the first line of every sample file. The text samples use the same syntax.
const provenanceHeader = "# provenance: "

// provenanceHeaderFor is the first line a file must carry for the records it holds: real when every record
// is real, invented when none is, and otherwise partly real with the numbers of the real records, counted
// from 1 over the records and not over the comment lines.
func provenanceHeaderFor(records []keyedSample) string {
	var real []string
	for i, s := range records {
		if s.Provenance == "real" {
			real = append(real, strconv.Itoa(i+1))
		}
	}
	switch len(real) {
	case 0:
		return provenanceHeader + "invented"
	case len(records):
		return provenanceHeader + "real"
	}
	return provenanceHeader + "partly real; real records: " + strings.Join(real, ", ")
}

// checkProvenanceHeader compares the first line of a file with the header its records call for.
func checkProvenanceHeader(first string, records []keyedSample) error {
	want := provenanceHeaderFor(records)
	if !strings.HasPrefix(first, provenanceHeader) {
		return fmt.Errorf("first line %q is no provenance header, want %q", first, want)
	}
	value := strings.TrimPrefix(first, provenanceHeader)
	if value != "real" && value != "invented" && !strings.HasPrefix(value, "partly real; real records: ") {
		return fmt.Errorf("unknown provenance %q, want %q", value, want)
	}
	if first != want {
		return fmt.Errorf("header %q disagrees with the records, want %q", first, want)
	}
	return nil
}

// firstLines returns the first line of every keyed sample file by branch name.
func firstLines(tb testing.TB) map[string]string {
	tb.Helper()
	entries, err := os.ReadDir(keyedSamplesDir)
	if err != nil {
		tb.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(keyedSamplesDir, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		first, _, _ := strings.Cut(string(raw), "\n")
		out[strings.TrimSuffix(e.Name(), ".jsonl")] = first
	}
	return out
}

func TestKeyedFixtures_EveryFileCarriesAProvenanceHeaderThatMatchesItsRecords(t *testing.T) {
	first := firstLines(t)
	for file, records := range loadKeyedSamples(t) {
		if err := checkProvenanceHeader(first[file], records); err != nil {
			t.Errorf("%s.jsonl: %v", file, err)
		}
	}
}

func TestKeyedFixtures_TheProvenanceCheckRefusesABadHeader(t *testing.T) {
	two := []keyedSample{{Provenance: "real"}, {Provenance: "real"}, {Provenance: "invented"}}
	allReal := []keyedSample{{Provenance: "real"}, {Provenance: "real"}}
	noneReal := []keyedSample{{Provenance: "invented"}}
	cases := []struct {
		name    string
		first   string
		records []keyedSample
		wantErr bool
	}{
		{"all real", "# provenance: real", allReal, false},
		{"none real", "# provenance: invented", noneReal, false},
		{"mixed", "# provenance: partly real; real records: 1, 2", two, false},
		{"missing header", "# One JSON record per line", two, true},
		{"empty first line", "", two, true},
		{"unknown value", "# provenance: mostly real", two, true},
		{"says real but one is invented", "# provenance: real", two, true},
		{"says invented but two are real", "# provenance: invented", two, true},
		{"says partly real without the list", "# provenance: partly real", two, true},
		{"wrong list", "# provenance: partly real; real records: 1, 3", two, true},
		{"list too short", "# provenance: partly real; real records: 1", two, true},
		{"partly real but all are real", "# provenance: partly real; real records: 1, 2", allReal, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkProvenanceHeader(c.first, c.records); (err != nil) != c.wantErr {
				t.Errorf("checkProvenanceHeader(%q) error = %v, want an error: %v", c.first, err, c.wantErr)
			}
		})
	}
}

func TestKeyedFixtures_EveryRecordIsClaimedAndParsedByItsOwnBranch(t *testing.T) {
	for file, records := range loadKeyedSamples(t) {
		t.Run(file, func(t *testing.T) {
			h := newHarness(t, keyedOnly(file))
			for _, s := range records {
				h.runBatch(keyedBatch(s))
			}
			if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
				t.Errorf("unmatched = %v", got)
			}
			if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
				t.Errorf("parse_failed = %v", got)
			}
			total := 0
			for _, ld := range h.sink.AllLogs() {
				total += ld.LogRecordCount()
			}
			if total != len(records) {
				t.Errorf("%d records out, %d in", total, len(records))
			}
		})
	}
}

// shaped runs one record through a config and returns what came out.
func shaped(t *testing.T, cfg *Config, s keyedSample) outRecord {
	t.Helper()
	out := flatten(newHarness(t, cfg).runBatch(keyedBatch(s)))
	if len(out) != 1 {
		t.Fatalf("%d records out, want 1", len(out))
	}
	return out[0]
}

func TestKeyedFixtures_NoOtherBranchTakesARecord(t *testing.T) {
	for file, records := range loadKeyedSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, s := range records {
				want := shaped(t, keyedOnly(file), s)
				got := shaped(t, defaultConfig(), s)
				if got.body != want.body || got.severity != want.severity || !reflect.DeepEqual(got.attrs, want.attrs) || !reflect.DeepEqual(got.resource, want.resource) {
					t.Errorf("record %d: every branch gave %q/%d %v, %s alone gave %q/%d %v",
						i+1, got.body, got.severity, got.attrs, file, want.body, want.severity, want.attrs)
				}
			}
		})
	}
}

func TestKeyedFixtures_SeverityTextAndNumberComeFromOneMapping(t *testing.T) {
	for file, records := range loadKeyedSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, s := range records {
				rec := shaped(t, defaultConfig(), s)
				if want := severity.FromNumber(int32(rec.severity)).Text; rec.severityText != want {
					t.Errorf("record %d: severity %d has text %q, the mapping gives %q", i+1, rec.severity, rec.severityText, want)
				}
			}
		})
	}
}

func TestKeyedFixtures_IntegerAttributesAreIntegersAndNoDeprecatedNameIsWritten(t *testing.T) {
	deprecated := rename.Deprecated()
	for file, records := range loadKeyedSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, s := range records {
				rec := shaped(t, defaultConfig(), s)
				for _, key := range rename.IntKeys {
					if v, ok := rec.attrs[key]; ok {
						if _, isInt := v.(int64); !isInt {
							t.Errorf("record %d: %s = %v (%T), want an integer", i+1, key, v, v)
						}
					}
				}
				for key := range rec.attrs {
					if slices.Contains(deprecated, key) {
						t.Errorf("record %d writes the deprecated name %s", i+1, key)
					}
				}
			}
		})
	}
}

// documentationIDs are the account numbers AWS uses in its documentation. They are not accounts of ours.
var documentationIDs = strings.NewReplacer("111122223333", "ACCOUNT", "123456789012", "ACCOUNT")

func TestKeyedFixtures_ScrubCheckFindsNoHostAccountOrSecret(t *testing.T) {
	for file, records := range loadKeyedSamples(t) {
		for i, s := range records {
			text := documentationIDs.Replace(s.line)
			for what, re := range scrub {
				if re.MatchString(text) {
					t.Errorf("%s.jsonl record %d holds a %s: %s", file, i+1, what, s.line)
				}
			}
		}
	}
}

// A batch that holds one record of every keyed format still gives every record its own result: the
// branches share no state between records of different formats.
func TestKeyedFixtures_AMixedBatchGivesTheSameResultAsOneRecordAtATime(t *testing.T) {
	samples := loadKeyedSamples(t)
	var all []keyedSample
	for _, file := range keyedBranches() {
		all = append(all, samples[file]...)
	}
	ld := plog.NewLogs()
	for _, s := range all {
		keyedBatch(s).ResourceLogs().MoveAndAppendTo(ld.ResourceLogs())
	}

	out := flatten(newHarness(t, defaultConfig()).runBatch(ld))

	if len(out) != len(all) {
		t.Fatalf("%d records out, %d in", len(out), len(all))
	}
	for i, s := range all {
		want := shaped(t, defaultConfig(), s)
		if out[i].body != want.body || out[i].severity != want.severity || !reflect.DeepEqual(out[i].attrs, want.attrs) || !reflect.DeepEqual(out[i].resource, want.resource) {
			t.Errorf("record %d in the mixed batch: %q/%d %v %v, alone %q/%d %v %v",
				i+1, out[i].body, out[i].severity, out[i].attrs, out[i].resource, want.body, want.severity, want.attrs, want.resource)
		}
	}
}
