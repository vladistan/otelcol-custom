// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func summaryRules() []DropRule {
	return []DropRule{{
		Name: "logons", Channels: []string{"Security"}, EventIDs: []string{"4624", "4634"},
		TallyBy: []string{"AuthenticationPackageName"},
	}}
}

func summaryHarness(t *testing.T, rules []DropRule) (*harness, *winlogProcessor, *fakeClock) {
	t.Helper()
	h := newHarnessCfg(t, &Config{DropRules: rules, DropSummary: DropSummaryConfig{Enabled: true}})
	p := h.proc.(*winlogProcessor)
	clock := &fakeClock{now: time.Date(2000, 1, 1, 10, 0, 0, 0, time.UTC)}
	p.summary.now = clock.Now
	return h, p, clock
}

func authEvent(id int64, pkg string) testEvent {
	return testEvent{
		channel: "Security", id: id, message: "an account was logged on, secret text",
		data: map[string]any{"AuthenticationPackageName": pkg},
	}
}

func feed(t *testing.T, p *winlogProcessor, events ...testEvent) {
	t.Helper()
	if err := p.ConsumeLogs(context.Background(), batch(events...)); err != nil {
		t.Fatalf("consume: %v", err)
	}
}

// summaryRecords picks the sink's records that carry winlog.drop_summary.total.
func summaryRecords(h *harness) []plog.LogRecord {
	var out []plog.LogRecord
	for _, logs := range h.sink.AllLogs() {
		for i := 0; i < logs.ResourceLogs().Len(); i++ {
			records := logs.ResourceLogs().At(i).ScopeLogs().At(0).LogRecords()
			for j := 0; j < records.Len(); j++ {
				if _, ok := records.At(j).Attributes().Get("winlog.drop_summary.total"); ok {
					out = append(out, records.At(j))
				}
			}
		}
	}
	return out
}

func summaryRule(t *testing.T, record plog.LogRecord, rule string) map[string]any {
	t.Helper()
	rules, ok := record.Attributes().Get("winlog.drop_summary.rules")
	if !ok {
		t.Fatal("winlog.drop_summary.rules is missing")
	}
	entry, ok := rules.Map().AsRaw()[rule].(map[string]any)
	if !ok {
		t.Fatalf("no summary entry for rule %q in %v", rule, rules.Map().AsRaw())
	}
	return entry
}

func TestNoSummaryIsEmittedWhenDisabled(t *testing.T) {
	h := newHarness(t, summaryRules())
	p := h.proc.(*winlogProcessor)
	feed(t, p, authEvent(4634, "Kerberos"))
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(summaryRecords(h)); got != 0 {
		t.Errorf("got %d summary records with the summary disabled", got)
	}
}

func TestSummaryWaitsForTheHourToPass(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"))
	clock.advance(59 * time.Minute)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := len(summaryRecords(h)); got != 0 {
		t.Errorf("got %d summary records before the hour was up", got)
	}
}

func TestSummaryIsEmittedOncePerHour(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"))
	clock.advance(time.Hour)
	for range 2 {
		if err := p.emitDue(context.Background()); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	if got := len(summaryRecords(h)); got != 1 {
		t.Errorf("got %d summary records, want 1", got)
	}
}

func TestSummaryCountsPerRuleEventIDAndFieldValue(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"), authEvent(4634, "Kerberos"), authEvent(4624, "NTLM"))
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	entry := summaryRule(t, summaryRecords(h)[0], "logons")
	want := map[string]any{
		"total":     int64(3),
		"event_ids": map[string]any{"4634": int64(2), "4624": int64(1)},
		"fields":    map[string]any{"AuthenticationPackageName": map[string]any{"Kerberos": int64(2), "NTLM": int64(1)}},
	}
	if fmt.Sprint(entry) != fmt.Sprint(want) {
		t.Errorf("entry = %v, want %v", entry, want)
	}
}

func TestSummaryKeepsTheTopTwentyValuesAndCountsTheRestAsOther(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	var events []testEvent
	for i := 0; i < 25; i++ {
		for range 30 - i {
			events = append(events, authEvent(4634, fmt.Sprintf("pkg%02d", i)))
		}
	}
	feed(t, p, events...)
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	values := summaryRule(t, summaryRecords(h)[0], "logons")["fields"].(map[string]any)["AuthenticationPackageName"].(map[string]any)
	if got := len(values); got != 21 {
		t.Fatalf("got %d entries, want 20 values plus other", got)
	}
	if got := values["other"]; got != int64(10+9+8+7+6) {
		t.Errorf("other = %v, want %d", got, 10+9+8+7+6)
	}
}

func TestSummaryCarriesCountsNeverEventText(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"))
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	record := summaryRecords(h)[0]
	dump := record.Body().AsString() + fmt.Sprint(record.Attributes().AsRaw())
	if strings.Contains(dump, "secret text") {
		t.Errorf("summary carries event text: %s", dump)
	}
}

func TestSummaryNamesTheWindowAndTheTotal(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"), authEvent(4634, "NTLM"))
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	record := summaryRecords(h)[0]
	if got := attrInt(t, record, "winlog.drop_summary.total"); got != 2 {
		t.Errorf("total = %d, want 2", got)
	}
	if got := attrString(t, record, "winlog.drop_summary.window_start"); got != "2000-01-01T10:00:00Z" {
		t.Errorf("window_start = %q", got)
	}
	if got := attrString(t, record, "winlog.drop_summary.window_end"); got != "2000-01-01T11:00:00Z" {
		t.Errorf("window_end = %q", got)
	}
}

func TestSummaryWindowRestartsAfterAFlush(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"))
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	feed(t, p, authEvent(4634, "NTLM"))
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	records := summaryRecords(h)
	if len(records) != 2 {
		t.Fatalf("got %d summary records, want 2", len(records))
	}
	if got := attrInt(t, records[1], "winlog.drop_summary.total"); got != 1 {
		t.Errorf("second window total = %d, want 1", got)
	}
}

func TestSummaryCountsAFieldAbsentFromTheEventAsMissing(t *testing.T) {
	h, p, clock := summaryHarness(t, summaryRules())
	feed(t, p, testEvent{channel: "Security", id: 4634})
	clock.advance(time.Hour)
	if err := p.emitDue(context.Background()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	values := summaryRule(t, summaryRecords(h)[0], "logons")["fields"].(map[string]any)["AuthenticationPackageName"].(map[string]any)
	if values["(missing)"] != int64(1) {
		t.Errorf("values = %v, want (missing): 1", values)
	}
}

func TestShutdownFlushesThePartialHour(t *testing.T) {
	h, p, _ := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4634, "Kerberos"), authEvent(4634, "Kerberos"))
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	records := summaryRecords(h)
	if len(records) != 1 {
		t.Fatalf("got %d summary records, want 1", len(records))
	}
	if got := attrInt(t, records[0], "winlog.drop_summary.total"); got != 2 {
		t.Errorf("total = %d, want 2", got)
	}
}

func TestShutdownWithNothingDroppedEmitsNoSummary(t *testing.T) {
	h, p, _ := summaryHarness(t, summaryRules())
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(summaryRecords(h)); got != 0 {
		t.Errorf("got %d summary records, want none", got)
	}
}

func TestSummaryDoesNotCountKeptEvents(t *testing.T) {
	h, p, _ := summaryHarness(t, summaryRules())
	feed(t, p, authEvent(4625, "Kerberos"))
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(summaryRecords(h)); got != 0 {
		t.Errorf("got %d summary records for a kept event", got)
	}
}

func TestStartedProcessorStopsCleanly(t *testing.T) {
	_, p, _ := summaryHarness(t, summaryRules())
	if err := p.Start(context.Background(), nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestTallyByNamesAreValidated(t *testing.T) {
	cases := []struct {
		name    string
		tallyBy []string
		wantErr string
	}{
		{"empty name", []string{""}, "tally_by field name is empty"},
		{"duplicate", []string{"A", "A"}, "tally_by lists A twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{DropRules: []DropRule{{Name: "r", EventIDs: []string{"1"}, TallyBy: tc.tallyBy}}}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestSummaryIsOffByDefault(t *testing.T) {
	if NewFactory().CreateDefaultConfig().(*Config).DropSummary.Enabled {
		t.Error("the default config enables the drop summary")
	}
}
