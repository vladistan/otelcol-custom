// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gopkg.in/yaml.v3"
)

const (
	crlf        = `\r\n` // the four characters the console puts at the end of every fragment
	escapedAttr = "gcp.label.is_text_escaped"
	nodeAttr    = "gcp.label.instance_id"
)

// serialRules sets the options of the scrape reference config.
func serialRules(cfg *Config) {
	cfg.CombineField = "body"
	cfg.SourceIdentifier = "resource." + nodeAttr
	cfg.ContainerIdentifier = ""
	cfg.IsFirstEntryPattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+\+0000 [^ ]+ [^ ]+\[[0-9]+\]:`
	cfg.RequireAttribute = escapedAttr
	cfg.EmptyPatterns = []string{`^\\r\\n$`}
	cfg.NeverGluePatterns = []string{
		`^\[ *[0-9]+\.[0-9]+\]`,
		`\\x1b`,
		`^ *(Stopping|Starting|Unmounting|Mounting|Started|Stopped|Reached|Finished) `,
		`^(ssh-(rsa|ed25519)|ecdsa-sha2-|-----(BEGIN|END) )`,
		`^<[0-9]+>[A-Z][a-z]{2} `,
		`^[0-9A-F]{4}: `,
	}
	cfg.MinPreviousLength = 515
	cfg.FragmentSuffix = crlf
	cfg.GapTimeout = 2 * time.Second
	cfg.MaxTimeout = 10 * time.Second
}

// padded returns text of exactly n characters: head, filler and the console end.
func padded(head string, n int) string {
	fill := n - len(head) - len(crlf)
	if fill < 0 {
		fill = 0
	}
	return head + strings.Repeat("x", fill) + crlf
}

func tagged(n int) string { return padded("2026-01-01T00:00:00+0000 node-1 gcfsd-v2[100]: ", n) }
func cont(n int) string   { return padded("", n) }

// glueRow is one record of the glue tests.
type glueRow struct {
	node, uid, text string
	attr            any // value of the required attribute; nil leaves it out
}

func (r glueRow) logs() plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr(nodeAttr, r.node)
	rec := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	rec.Body().SetStr(r.text)
	if r.attr != nil {
		switch v := r.attr.(type) {
		case bool:
			rec.Attributes().PutBool(escapedAttr, v)
		case string:
			rec.Attributes().PutStr(escapedAttr, v)
		}
	}
	if r.uid != "" {
		rec.Attributes().PutStr(uidAttribute, r.uid)
	}
	rec.Attributes().PutStr(originalAttribute, r.text)
	return ld
}

func row(node, uid, text string) glueRow {
	return glueRow{node: node, uid: uid, text: text, attr: true}
}

type glueHarness struct {
	p      *logsProcessor
	sink   *recordSink
	reader *sdkmetric.ManualReader
}

func newGlueHarness(t *testing.T, mutate func(*Config)) *glueHarness {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	serialRules(cfg)
	if mutate != nil {
		mutate(cfg)
	}
	require.NoError(t, cfg.Validate())
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sink := &recordSink{}
	p, err := newLogsProcessor(set, cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.Start(context.Background(), nil))
	return &glueHarness{p: p, sink: sink, reader: reader}
}

func (h *glueHarness) send(t *testing.T, rows ...glueRow) {
	t.Helper()
	for _, r := range rows {
		require.NoError(t, h.p.ConsumeLogs(context.Background(), r.logs()))
	}
}

// finish stops the processor, which passes on every open sequence, and returns the output texts.
func (h *glueHarness) finish(t *testing.T) []plog.LogRecord {
	t.Helper()
	require.NoError(t, h.p.Shutdown(context.Background()))
	return h.sink.records()
}

func (h *glueHarness) counter(name string) int64 {
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		return -1
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

func texts(recs []plog.LogRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Body().Str()
	}
	return out
}

func TestGlue_FragmentsJoinWithoutTheInteriorEnd(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", tagged(600)), row("a", "2", cont(600)), row("a", "3", cont(100)))
	got := h.finish(t)

	require.Len(t, got, 1)
	want := strings.TrimSuffix(tagged(600), crlf) + strings.TrimSuffix(cont(600), crlf) + cont(100)
	assert.Equal(t, want, got[0].Body().Str())
	assert.Equal(t, 1, strings.Count(got[0].Body().Str(), crlf), "only the end of the last fragment stays")
	assert.EqualValues(t, 1, h.counter(gluedMetric))
}

func TestGlue_OriginalFollowsTheJoinedText(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", tagged(600)), row("a", "2", cont(100)))
	got := h.finish(t)

	require.Len(t, got, 1)
	orig, ok := got[0].Attributes().Get(originalAttribute)
	require.True(t, ok)
	assert.Equal(t, got[0].Body().Str(), orig.Str())
	assert.Equal(t, "1", mustStr(t, got[0], uidAttribute), "the record of the first fragment is kept")
}

func mustStr(t *testing.T, rec plog.LogRecord, key string) string {
	t.Helper()
	v, ok := rec.Attributes().Get(key)
	require.True(t, ok, key)
	return v.Str()
}

func TestGlue_SourceKeyFromAResourceAttributeAndFromADottedRecordAttribute(t *testing.T) {
	rows := func(h *glueHarness) []plog.LogRecord {
		h.send(t, row("a", "1", tagged(600)), row("b", "1", tagged(600)), row("a", "2", cont(100)), row("b", "2", cont(100)))
		return h.finish(t)
	}
	fromResource := rows(newGlueHarness(t, nil))
	assert.Len(t, fromResource, 2)

	// The same key as a record attribute whose name holds dots.
	h := newGlueHarness(t, func(c *Config) { c.SourceIdentifier = "attributes." + nodeAttr })
	for _, r := range []glueRow{row("a", "1", tagged(600)), row("b", "1", tagged(600)), row("a", "2", cont(100)), row("b", "2", cont(100))} {
		ld := r.logs()
		rec := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
		rec.Attributes().PutStr(nodeAttr, r.node)
		ld.ResourceLogs().At(0).Resource().Attributes().Remove(nodeAttr)
		require.NoError(t, h.p.ConsumeLogs(context.Background(), ld))
	}
	assert.Len(t, h.finish(t), 2)
}

func TestGlue_InterleavedNodesGiveOneRecordEach(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t,
		row("a", "1", tagged(600)), row("b", "1", tagged(700)),
		row("a", "2", cont(600)), row("b", "2", cont(100)),
		row("a", "3", cont(50)))
	got := texts(h.finish(t))

	require.Len(t, got, 2)
	assert.ElementsMatch(t, []string{
		strings.TrimSuffix(tagged(600), crlf) + strings.TrimSuffix(cont(600), crlf) + cont(50),
		strings.TrimSuffix(tagged(700), crlf) + cont(100),
	}, got)
}

func TestGlue_ASequenceIsPassedOnAtTheSizeCap(t *testing.T) {
	h := newGlueHarness(t, func(c *Config) { c.MaxTextSize = 1000 })
	h.send(t, row("a", "1", tagged(600)), row("a", "2", cont(600)), row("a", "3", cont(100)))
	got := texts(h.finish(t))

	assert.Equal(t, []string{
		tagged(600),
		strings.TrimSuffix(cont(600), crlf) + cont(100),
	}, got)
}

func TestGlue_ASequenceIsPassedOnAfterTheGap(t *testing.T) {
	h := newGlueHarness(t, func(c *Config) { c.GapTimeout = 40 * time.Millisecond })
	h.send(t, row("a", "1", tagged(600)))
	require.Eventually(t, func() bool { return len(h.sink.records()) == 1 }, 2*time.Second, 10*time.Millisecond)

	h.send(t, row("a", "2", cont(100)))
	got := texts(h.finish(t))
	assert.Equal(t, []string{tagged(600), cont(100)}, got, "the late fragment finds no sequence and is short, so it stands alone")
}

func TestGlue_ASequenceIsPassedOnAfterTheMaxTimeout(t *testing.T) {
	h := newGlueHarness(t, func(c *Config) { c.GapTimeout = 10 * time.Second; c.MaxTimeout = 100 * time.Millisecond })
	h.send(t, row("a", "1", tagged(600)))
	for i := 2; i <= 6; i++ {
		time.Sleep(30 * time.Millisecond)
		h.send(t, row("a", string(rune('0'+i)), cont(600)))
	}
	require.Eventually(t, func() bool { return len(h.sink.records()) >= 1 }, 2*time.Second, 10*time.Millisecond,
		"the age of the sequence passes it on although no gap is long")
	_ = h.finish(t)
}

func TestGlue_AnOpenSequenceIsPassedOnAtShutdown(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", tagged(600)), row("a", "2", cont(100)))
	assert.Empty(t, h.sink.records(), "nothing leaves before the gap, the cap or the shutdown")
	assert.Len(t, h.finish(t), 1)
}

func TestGlue_ARecordWithoutTheAttributePassesUntouchedAndLeavesTheSequenceOpen(t *testing.T) {
	h := newGlueHarness(t, nil)
	other := glueRow{node: "a", uid: "9", text: "plain line", attr: nil}
	h.send(t, row("a", "1", tagged(600)), other, row("a", "2", cont(100)))
	got := texts(h.finish(t))

	assert.Equal(t, []string{"plain line", strings.TrimSuffix(tagged(600), crlf) + cont(100)}, got)
}

func TestGlue_TheAttributeCanBeABoolOrTheText(t *testing.T) {
	for name, tc := range map[string]struct {
		attr any
		want int
	}{"bool true": {true, 1}, "text true": {"true", 1}, "text True": {"True", 1}, "bool false": {false, 2}, "text no": {"no", 2}} {
		t.Run(name, func(t *testing.T) {
			h := newGlueHarness(t, nil)
			h.send(t,
				glueRow{node: "a", uid: "1", text: tagged(600), attr: tc.attr},
				glueRow{node: "a", uid: "2", text: cont(100), attr: tc.attr})
			assert.Len(t, h.finish(t), tc.want)
		})
	}
}

func TestGlue_StandaloneLinesAreNeverGluedAndEndTheSequence(t *testing.T) {
	standalone := map[string]string{
		"kernel":  `[    1.234567] loaded module` + crlf,
		"console": `[\x1b[0;32m  OK  \x1b[0m] Started thing.` + crlf,
		"systemd": `Stopping thing.` + crlf,
		"ssh":     `ssh-ed25519 AAAAexample root@node-1` + crlf,
		"syslog":  `<14>Jan 01 00:00:00 cloud-init: line` + crlf,
		"hexdump": `0010: 00 01 02 03` + crlf,
	}
	for name, line := range standalone {
		t.Run(name, func(t *testing.T) {
			h := newGlueHarness(t, nil)
			h.send(t, row("a", "1", tagged(600)), row("a", "2", line), row("a", "3", cont(600)), row("a", "4", cont(100)))
			got := texts(h.finish(t))

			assert.Equal(t, []string{
				tagged(600),
				line,
				strings.TrimSuffix(cont(600), crlf) + cont(100),
			}, got, "the long row after the standalone line opens a sequence of its own")
		})
	}
}

func TestGlue_AShortUntaggedRowAfterAShortTaggedRowIsNotGlued(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", tagged(140)), row("a", "2", cont(361)))
	assert.Equal(t, []string{tagged(140), cont(361)}, texts(h.finish(t)))
}

func TestGlue_ALongUntaggedRowOpensASequenceOfItsOwn(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", cont(943)), row("a", "2", cont(6)))
	assert.Equal(t, []string{strings.TrimSuffix(cont(943), crlf) + cont(6)}, texts(h.finish(t)))
}

func TestGlue_ABareLineEndsTheSequenceAndIsDropped(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t,
		row("a", "1", tagged(600)), row("a", "2", crlf), row("a", "3", cont(600)),
		row("b", "1", crlf))
	got := texts(h.finish(t))

	assert.Equal(t, []string{tagged(600), cont(600)}, got, "a bare line does not join the rows on both sides")
	assert.EqualValues(t, 2, h.counter(emptyDroppedMetric), "one bare line ended a sequence, the other had none open")
}

func TestGlue_ARecordWithNoTextIsDroppedLikeABareLine(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "1", tagged(600)), row("a", "2", ""))
	assert.Equal(t, []string{tagged(600)}, texts(h.finish(t)))
	assert.EqualValues(t, 1, h.counter(emptyDroppedMetric))
}

func TestGlue_AUidThatDoesNotRiseIsCountedAndStillGlues(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "5", tagged(600)), row("a", "3", cont(600)), row("a", "3", cont(100)), row("b", "1", tagged(100)))
	got := h.finish(t)

	assert.Len(t, got, 2)
	assert.EqualValues(t, 2, h.counter(uidNotRisingMetric), "a lower uid and an equal one")
	for _, rec := range got {
		rec.Attributes().Range(func(k string, _ pcommon.Value) bool {
			assert.False(t, strings.HasPrefix(k, "gcp.split."), "the glue reads and writes no split attribute: %s", k)
			return true
		})
	}
}

func TestGlue_TheUIDCountersOfTwoNodesAreNotCompared(t *testing.T) {
	h := newGlueHarness(t, nil)
	h.send(t, row("a", "50", tagged(100)), row("b", "1", tagged(100)))
	_ = h.finish(t)
	assert.EqualValues(t, 0, h.counter(uidNotRisingMetric))
}

func TestGlue_ARecordWithoutTheNodeKeyPassesUntouched(t *testing.T) {
	h := newGlueHarness(t, nil)
	ld := row("a", "1", tagged(600)).logs()
	ld.ResourceLogs().At(0).Resource().Attributes().Remove(nodeAttr)
	require.NoError(t, h.p.ConsumeLogs(context.Background(), ld))
	assert.Equal(t, []string{tagged(600)}, texts(h.sink.records()))
	_ = h.finish(t)
}

func TestGlue_AMapBodyIsLeftAlone(t *testing.T) {
	h := newGlueHarness(t, nil)
	ld := row("a", "1", "x").logs()
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().SetEmptyMap().PutStr("text", tagged(600))
	require.NoError(t, h.p.ConsumeLogs(context.Background(), ld))
	got := h.sink.records()
	require.Len(t, got, 1)
	assert.Equal(t, pcommon.ValueTypeMap, got[0].Body().Type())
	_ = h.finish(t)
}

func TestConfig_GlueOptionsNeedTheRequiredAttributeAndHaveToBeValid(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"patterns without the attribute": {func(c *Config) { c.NeverGluePatterns = []string{`x`} }, "need require_attribute"},
		"suffix without the attribute":   {func(c *Config) { c.FragmentSuffix = crlf }, "need require_attribute"},
		"bad never glue pattern":         {func(c *Config) { serialRules(c); c.NeverGluePatterns = []string{`(`} }, "never_glue_patterns[0]"},
		"bad empty pattern":              {func(c *Config) { serialRules(c); c.EmptyPatterns = []string{`[`} }, "empty_patterns[0]"},
		"negative length":                {func(c *Config) { serialRules(c); c.MinPreviousLength = -1 }, "min_previous_length"},
		"no size":                        {func(c *Config) { serialRules(c); c.MaxTextSize = 0 }, "max_text_size"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			tc.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	assert.NoError(t, createDefaultConfig().(*Config).Validate(), "the default config has no glue option on")
}

func TestZeroConfigKeepsTheOldBehaviour(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.Empty(t, cfg.RequireAttribute)
	p, sink := startProcessor(t, nil)
	send(t, p, journalLog("", "", "42", "2026-11-15T14:00:01 service failed"))
	send(t, p, journalLog("", "", "42", "  detail line"))
	settle()
	assert.Equal(t, []outRecord{{"", "2026-11-15T14:00:01 service failed\n  detail line"}}, sink.records())
}

// fixtureRow is a line of testdata/serial-fragments.jsonl.
type fixtureRow struct {
	Node string `json:"node"`
	UID  string `json:"uid"`
	Text string `json:"text"`
}

func loadFixture(t *testing.T) (header []string, rows []fixtureRow) {
	t.Helper()
	f, err := os.Open("testdata/serial-fragments.jsonl")
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			header = append(header, line)
			continue
		}
		var r fixtureRow
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		rows = append(rows, r)
	}
	require.NoError(t, sc.Err())
	return header, rows
}

func TestGlue_TheFixtureCarriesItsProvenance(t *testing.T) {
	header, rows := loadFixture(t)
	require.NotEmpty(t, header)
	assert.Equal(t, "# provenance: real", header[0])
	assert.Len(t, rows, 75)
	for _, r := range rows {
		assert.True(t, strings.HasSuffix(r.Text, crlf), "uid %s: every row ends with the console end", r.UID)
	}
}

var shaToken = regexp.MustCompile(`sha256:[0-9a-f]+`)

// The fixture holds 75 rows: 7 bare lines, which the glue drops, and 68 others that make 48 records, 7 of
// them from two or more fragments (up to 8). A console line interrupts one hash list inside a digest, and
// that digest stays cut: no rule can join across a line that is not a fragment.
const (
	fixtureRecords  = 48
	fixtureGlued    = 7
	fixtureBare     = 7
	fixtureCutAfter = 1
)

func TestGlue_TheRowsOfOneNodeGiveWholeLines(t *testing.T) {
	_, rows := loadFixture(t)
	h := newGlueHarness(t, nil)
	var cutBefore int
	for _, r := range rows {
		for _, m := range shaToken.FindAllString(strings.TrimSuffix(r.Text, crlf), -1) {
			if len(m) != len("sha256:")+64 {
				cutBefore++
			}
		}
		h.send(t, row(r.Node, r.UID, r.Text))
	}
	require.Equal(t, 6, cutBefore, "the fixture has hash lists cut inside a digest")
	got := h.finish(t)

	var cutAfter int
	for _, rec := range got {
		text := rec.Body().Str()
		assert.NotEqual(t, crlf, text)
		assert.Equal(t, 1, strings.Count(text, crlf), "only the end of the last fragment stays")
		for _, m := range shaToken.FindAllString(strings.TrimSuffix(text, crlf), -1) {
			if len(m) != len("sha256:")+64 {
				cutAfter++
			}
		}
	}
	assert.Equal(t, fixtureCutAfter, cutAfter, "digests still cut after the join")
	assert.Len(t, got, fixtureRecords)
	assert.EqualValues(t, fixtureBare, h.counter(emptyDroppedMetric))
	assert.EqualValues(t, fixtureGlued, h.counter(gluedMetric))
	assert.EqualValues(t, 0, h.counter(uidNotRisingMetric))
}

func TestGlue_StandaloneRowsOfTheFixtureStayWhole(t *testing.T) {
	_, rows := loadFixture(t)
	h := newGlueHarness(t, nil)
	never := regexp.MustCompile(`^\[ *[0-9]+\.[0-9]+\]|\\x1b|^<[0-9]+>[A-Z][a-z]{2} |^(ssh-(rsa|ed25519)|ecdsa-sha2-|-----(BEGIN|END) )`)
	want := map[string]int{}
	for _, r := range rows {
		h.send(t, row(r.Node, r.UID, r.Text))
		if never.MatchString(r.Text) {
			want[r.Text]++
		}
	}
	got := map[string]int{}
	for _, rec := range h.finish(t) {
		if never.MatchString(rec.Body().Str()) {
			got[rec.Body().Str()]++
		}
	}
	assert.NotEmpty(t, want)
	assert.Equal(t, want, got, "kernel, console, ssh and syslog rows come out as they went in")
}

// referenceBlock reads the logrecombine/serial block of the scrape reference config.
func referenceBlock(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../test/configs/scrape.yaml")
	require.NoError(t, err)
	var doc struct {
		Processors map[string]map[string]any `yaml:"processors"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	block, ok := doc.Processors["logrecombine/serial"]
	require.True(t, ok, "scrape.yaml defines logrecombine/serial")
	return block
}

func TestReferenceConfig_EveryKeyOfTheSerialBlockIsAnOptionAndTheBlockIsValid(t *testing.T) {
	block := referenceBlock(t)
	options := map[string]bool{}
	typ := reflect.TypeOf(Config{})
	for i := range typ.NumField() {
		options[typ.Field(i).Tag.Get("mapstructure")] = true
	}
	for key := range block {
		assert.True(t, options[key], "%s is not an option of the processor", key)
	}

	cfg := createDefaultConfig().(*Config)
	strs := func(key string) []string {
		var out []string
		for _, v := range block[key].([]any) {
			out = append(out, v.(string))
		}
		return out
	}
	cfg.CombineField = block["combine_field"].(string)
	cfg.SourceIdentifier = block["source_identifier"].(string)
	cfg.ContainerIdentifier = block["container_identifier"].(string)
	cfg.RequireAttribute = block["require_attribute"].(string)
	cfg.IsFirstEntryPattern = block["is_first_entry_pattern"].(string)
	cfg.EmptyPatterns = strs("empty_patterns")
	cfg.NeverGluePatterns = strs("never_glue_patterns")
	cfg.MinPreviousLength = block["min_previous_length"].(int)
	cfg.FragmentSuffix = block["fragment_suffix"].(string)
	cfg.MaxTextSize = block["max_text_size"].(int)
	var err error
	cfg.GapTimeout, err = time.ParseDuration(block["gap_timeout"].(string))
	require.NoError(t, err)
	cfg.MaxTimeout, err = time.ParseDuration(block["max_timeout"].(string))
	require.NoError(t, err)

	require.NoError(t, cfg.Validate())
	_, err = newLogsProcessor(processortest.NewNopSettings(component.MustNewType(typeStr)), cfg, &recordSink{})
	require.NoError(t, err)
}

func TestReferenceConfig_TheSerialBlockGivesTheFixtureResultsOfTheTests(t *testing.T) {
	block := referenceBlock(t)
	cfg := createDefaultConfig().(*Config)
	serialRules(cfg)
	assert.Equal(t, block["is_first_entry_pattern"], cfg.IsFirstEntryPattern)
	assert.Equal(t, block["min_previous_length"], cfg.MinPreviousLength)
	assert.Equal(t, block["fragment_suffix"], cfg.FragmentSuffix)
	assert.Equal(t, block["require_attribute"], cfg.RequireAttribute)
	for i, v := range block["never_glue_patterns"].([]any) {
		assert.Equal(t, v, cfg.NeverGluePatterns[i])
	}
	assert.Equal(t, block["empty_patterns"].([]any)[0], cfg.EmptyPatterns[0])
}
