// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/rename"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

const samplesDir = "testdata/samples"

// sampleFile is one text sample file: its sample lines and what its marker says about them.
type sampleFile struct {
	lines      []string
	provenance string // real, partly real or invented
	realLines  []int  // 1-based numbers of the sample lines that are real, for partly real
}

const (
	provenancePrefix = "# provenance:"
	realLinesLabel   = "real lines:"
)

// parseSampleFile reads one sample file. Lines that start with "#" are comments and not records; the
// file must hold exactly one "# provenance:" comment. A leading space is part of a sample line.
func parseSampleFile(raw []byte) (sampleFile, error) {
	var f sampleFile
	var marker string
	markers := 0
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, provenancePrefix):
			markers++
			marker = strings.TrimSpace(strings.TrimPrefix(line, provenancePrefix))
		case strings.HasPrefix(line, "#"):
			// another comment: the evidence note or a spell-check word list
		case strings.TrimSpace(line) != "":
			f.lines = append(f.lines, line)
		}
	}
	switch {
	case markers == 0:
		return f, errors.New("no # provenance: line")
	case markers > 1:
		return f, errors.New("more than one # provenance: line")
	case len(f.lines) == 0:
		return f, errors.New("no sample line")
	}
	value, list, hasList := strings.Cut(marker, ";")
	f.provenance = strings.TrimSpace(value)
	list = strings.TrimSpace(list)
	switch f.provenance {
	case "real", "invented":
		if hasList {
			return f, fmt.Errorf("%q: real lines is only for partly real", marker)
		}
	case "partly real":
		nums, err := parseRealLines(list, len(f.lines))
		if err != nil {
			return f, err
		}
		f.realLines = nums
	default:
		return f, fmt.Errorf("unknown value %q: want real, partly real or invented", f.provenance)
	}
	return f, nil
}

// parseRealLines reads "real lines: 1, 3-4" against a file of n sample lines.
func parseRealLines(list string, n int) ([]int, error) {
	rest, ok := strings.CutPrefix(list, realLinesLabel)
	if !ok || strings.TrimSpace(rest) == "" {
		return nil, errors.New(`partly real needs "real lines: 1, 3-4"`)
	}
	var out []int
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		first, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("%q is not a line number", part)
		}
		last := first
		if isRange {
			if last, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("%q is not a line number", part)
			}
		}
		if last < first {
			return nil, fmt.Errorf("%q runs backwards", part)
		}
		if first < 1 || last > n {
			return nil, fmt.Errorf("%q is beyond the %d sample lines", part, n)
		}
		for i := first; i <= last; i++ {
			out = append(out, i)
		}
	}
	if len(out) == n {
		return nil, errors.New("every line is listed as real: mark the file real")
	}
	return out, nil
}

// loadSampleFiles reads every text sample file with its marker.
func loadSampleFiles(tb testing.TB) map[string]sampleFile {
	tb.Helper()
	entries, err := os.ReadDir(samplesDir)
	if err != nil {
		tb.Fatalf("read %s: %v", samplesDir, err)
	}
	out := map[string]sampleFile{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(samplesDir, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		f, err := parseSampleFile(raw)
		if err != nil {
			tb.Fatalf("%s: %v", e.Name(), err)
		}
		out[strings.TrimSuffix(e.Name(), ".txt")] = f
	}
	return out
}

// loadSamples reads the sample lines of every format. The marker and other comment lines are not records.
func loadSamples(tb testing.TB) map[string][]string {
	tb.Helper()
	out := map[string][]string{}
	for name, f := range loadSampleFiles(tb) {
		out[name] = f.lines
	}
	return out
}

func batchOf(lines []string) plog.Logs {
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for _, l := range lines {
		sl.LogRecords().AppendEmpty().Body().SetStr(l)
	}
	return ld
}

// branchOf names the branch a sample file belongs to: the file name, except logrus which also reads zap and slog.
func branchOf(file string) string { return file }

// sourceKeyedBranches claim a record by its log.source attribute, so a bare text line cannot reach them.
var sourceKeyedBranches = []string{"gcplb", "awselb", "s3access", "gcpaudit", "eksaudit", "cloudtrail"}

func TestFixtures_EveryTextBranchHasASampleFile(t *testing.T) {
	samples := loadSamples(t)
	for _, name := range branchNames {
		if name == "flow" || slices.Contains(sourceKeyedBranches, name) || attributeKeyed[name] || journalKeyed[name] {
			continue // claimed by attributes, not text: their samples are the JSON-lines files in testdata/samples-keyed
		}
		if len(samples[name]) == 0 {
			t.Errorf("no sample file for branch %s", name)
		}
	}
	for file := range samples {
		if !slices.Contains(branchNames, branchOf(file)) {
			t.Errorf("sample file %s.txt names no branch", file)
		}
	}
}

func TestFixtures_EveryLineIsClaimedAndParsedByItsOwnBranch(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Branches = []string{branchOf(file)}
			h := newHarness(t, cfg)

			out := flatten(h.runBatch(batchOf(lines)))

			if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
				t.Errorf("unmatched = %v", got)
			}
			if got := h.counter(parseFailedMetric, "format"); len(got) != 0 {
				t.Errorf("parse_failed = %v", got)
			}
			if len(out) != len(lines) {
				t.Errorf("%d records out, %d lines in", len(out), len(lines))
			}
		})
	}
}

func TestFixtures_NoOtherBranchTakesALine(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			alone := defaultConfig()
			alone.Branches = []string{branchOf(file)}
			want := flatten(newHarness(t, alone).runBatch(batchOf(lines)))

			got := flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines)))

			if len(got) != len(want) {
				t.Fatalf("%d records with every branch, %d with one", len(got), len(want))
			}
			for i := range want {
				if got[i].body != want[i].body || got[i].severity != want[i].severity {
					t.Errorf("line %d: with every branch %q/%d, alone %q/%d", i, got[i].body, got[i].severity, want[i].body, want[i].severity)
				}
			}
		})
	}
}

func TestFixtures_SeverityTextAndNumberComeFromOneMapping(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, rec := range flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))) {
				want := severity.FromNumber(int32(rec.severity)).Text
				if rec.severityText != want {
					t.Errorf("line %d: severity %d has text %q, the mapping gives %q", i, rec.severity, rec.severityText, want)
				}
			}
		})
	}
}

func TestFixtures_IntegerAttributesAreIntegers(t *testing.T) {
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, rec := range flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))) {
				for _, key := range rename.IntKeys {
					v, ok := rec.attrs[key]
					if !ok {
						continue
					}
					if _, isInt := v.(int64); !isInt {
						t.Errorf("line %d: %s = %v (%T), want an integer", i, key, v, v)
					}
				}
			}
		})
	}
}

func TestFixtures_NoDeprecatedNameIsWritten(t *testing.T) {
	deprecated := rename.Deprecated()
	for file, lines := range loadSamples(t) {
		t.Run(file, func(t *testing.T) {
			for i, rec := range flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))) {
				for key := range rec.attrs {
					if slices.Contains(deprecated, key) {
						t.Errorf("line %d writes the deprecated name %s", i, key)
					}
				}
			}
		})
	}
}

func TestFixtures_TheRawLineStaysInLogRecordOriginal(t *testing.T) {
	for file, lines := range loadSamples(t) {
		if file == "cisco" || file == "syslog" || file == "k8s" {
			continue // these branches keep the raw line only when a source attribute held it
		}
		t.Run(file, func(t *testing.T) {
			for i, rec := range flatten(newHarness(t, defaultConfig()).runBatch(batchOf(lines))) {
				if got, _ := rec.attrs["log.record.original"].(string); !slices.Contains(lines, got) {
					t.Errorf("line %d: log.record.original = %q, not one of the sample lines", i, got)
				}
			}
		})
	}
}

// scrub holds what a fixture must not contain: private addresses, account numbers, e-mail addresses,
// internal domain suffixes and secrets. Internal names are checked by scripts/check-public-prose.sh.
var scrub = map[string]*regexp.Regexp{
	"private IPv4 address":  regexp.MustCompile(`\b(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b`),
	"12 digit account id":   regexp.MustCompile(`\b\d{12}\b`),
	"e-mail address":        regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}`),
	"internal domain":       regexp.MustCompile(`\.(home|lan|local|internal|corp)(:|/|\s|"|'|,|;|$)|\.(home|lan|local|internal|corp)\.[a-z0-9-]+\.[a-z]{2,}`),
	"cloud access key":      regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	"private key":           regexp.MustCompile(`-----BEGIN`),
	"credential assignment": regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key)\s*[=:]\s*\S{8,}`),
	"long hex string":       regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
	"long base64 string":    regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
}

func TestFixtures_ScrubCheckFindsNoHostAccountOrSecret(t *testing.T) {
	for file, lines := range loadSamples(t) {
		for i, line := range lines {
			for what, re := range scrub {
				if re.MatchString(line) {
					t.Errorf("%s.txt line %d holds a %s: %q", file, i+1, what, line)
				}
			}
		}
	}
}

func TestFixtures_ScrubCheckCatchesWhatItNames(t *testing.T) {
	for what, bad := range map[string]string{
		"private IPv4 address":  "client 10.20.30.40 connected",
		"12 digit account id":   "account 123456789012 denied",
		"e-mail address":        "user someone@example.org logged in",
		"internal domain":       "host build01.corp up",
		"cloud access key":      "key AKIA" + "ABCDEFGHIJKLMNOP leaked",
		"private key":           "-----BEG" + "IN PRIV" + "ATE KEY-----",
		"credential assignment": "pass" + "word=hunter22222",
		"long hex string":       "id 0123456789abcdef0123456789abcdef",
		"long base64 string":    "blob QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5",
	} {
		if !scrub[what].MatchString(bad) {
			t.Errorf("the %s check does not match %q", what, bad)
		}
	}
}

func TestFixtures_ScrubCheckLetsAModulePathAndAnExampleHostPass(t *testing.T) {
	for _, ok := range []string{
		"view='sentry.web.frontend.home.HomeView'",
		"host switch-a.example.org up",
		"url http://example.org:8080/path",
	} {
		if scrub["internal domain"].MatchString(ok) {
			t.Errorf("the internal domain check flags %q", ok)
		}
	}
	for _, bad := range []string{"host switch-a.home.example.com up", "host nas.lan:445", "dns x.internal,"} {
		if !scrub["internal domain"].MatchString(bad) {
			t.Errorf("the internal domain check misses %q", bad)
		}
	}
}

func BenchmarkFormats(b *testing.B) {
	p := newHarnessB(b, defaultConfig())
	for file, lines := range loadSamples(b) {
		b.Run(file, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ld := plog.NewLogs()
				sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
				for i := range 1000 {
					sl.LogRecords().AppendEmpty().Body().SetStr(lines[i%len(lines)])
				}
				if err := p.ConsumeLogs(b.Context(), ld); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
