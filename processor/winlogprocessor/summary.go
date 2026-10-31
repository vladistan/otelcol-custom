// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	summaryInterval   = time.Hour
	summaryTopValues  = 20
	summaryMaxTracked = 1000 // distinct values kept per rule and field in one window
	missingValue      = "(missing)"
	otherValue        = "other"

	summaryAttrPrefix = "winlog.drop_summary."
	summaryLogSource  = "winlog:drop_summary"
)

// valueTally counts the values of one tally_by field. Once summaryMaxTracked
// distinct values are held, new values count under other.
type valueTally struct {
	counts map[string]int64
	other  int64
}

func (v *valueTally) add(value string) {
	if _, tracked := v.counts[value]; tracked || len(v.counts) < summaryMaxTracked {
		v.counts[value]++
		return
	}
	v.other++
}

// top returns the summaryTopValues most frequent values and one other count.
func (v *valueTally) top() map[string]any {
	type entry struct {
		value string
		n     int64
	}
	entries := make([]entry, 0, len(v.counts))
	for value, n := range v.counts {
		entries = append(entries, entry{value, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].value < entries[j].value
	})
	out := make(map[string]any, summaryTopValues+1)
	other := v.other
	for i, e := range entries {
		if i < summaryTopValues {
			out[e.value] = e.n
		} else {
			other += e.n
		}
	}
	if other > 0 {
		out[otherValue] = other
	}
	return out
}

type ruleTally struct {
	total  int64
	ids    map[string]int64
	fields map[string]*valueTally
}

// dropSummary counts dropped events per rule, event ID and tally_by value for
// one window. It holds counts, never event text.
type dropSummary struct {
	mu    sync.Mutex
	now   func() time.Time
	start time.Time
	rules map[string]*ruleTally
}

func newDropSummary() *dropSummary {
	return &dropSummary{now: time.Now}
}

func (s *dropSummary) add(r *rule, f *eventFields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rules == nil {
		s.start = s.now()
		s.rules = make(map[string]*ruleTally)
	}
	tally, ok := s.rules[r.name]
	if !ok {
		tally = &ruleTally{ids: make(map[string]int64), fields: make(map[string]*valueTally)}
		s.rules[r.name] = tally
	}
	tally.total++
	if f.hasID {
		tally.ids[strconv.FormatUint(uint64(f.id), 10)]++
	}
	for _, name := range r.tallyBy {
		values, ok := tally.fields[name]
		if !ok {
			values = &valueTally{counts: make(map[string]int64)}
			tally.fields[name] = values
		}
		value, found := f.eventDataValue(name)
		if !found {
			value = missingValue
		}
		values.add(value)
	}
}

// take returns the window as a log record and starts a new window. It returns
// false when nothing was dropped, or, unless force is set, the window is not
// yet an hour old.
func (s *dropSummary) take(force bool) (plog.Logs, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.rules == nil || (!force && now.Sub(s.start) < summaryInterval) {
		return plog.Logs{}, false
	}
	logs := buildSummary(s.rules, s.start, now)
	s.rules = nil
	return logs, true
}

func buildSummary(rules map[string]*ruleTally, start, end time.Time) plog.Logs {
	var total int64
	byRule := make(map[string]any, len(rules))
	for name, tally := range rules {
		total += tally.total
		ids := make(map[string]any, len(tally.ids))
		for id, n := range tally.ids {
			ids[id] = n
		}
		fields := make(map[string]any, len(tally.fields))
		for field, values := range tally.fields {
			fields[field] = values.top()
		}
		byRule[name] = map[string]any{"total": tally.total, "event_ids": ids, "fields": fields}
	}
	logs := plog.NewLogs()
	scope := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	scope.Scope().SetName(scopeName)
	record := scope.LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(end))
	record.SetObservedTimestamp(pcommon.NewTimestampFromTime(end))
	record.SetSeverityNumber(plog.SeverityNumberInfo)
	record.SetSeverityText("INFO")
	record.Body().SetStr("winlog drop summary: " + strconv.FormatInt(total, 10) + " events dropped")
	attrs := record.Attributes()
	attrs.PutStr("log.source", summaryLogSource)
	attrs.PutStr(summaryAttrPrefix+"window_start", start.UTC().Format(time.RFC3339))
	attrs.PutStr(summaryAttrPrefix+"window_end", end.UTC().Format(time.RFC3339))
	attrs.PutInt(summaryAttrPrefix+"total", total)
	if err := attrs.PutEmptyMap(summaryAttrPrefix + "rules").FromRaw(byRule); err != nil {
		panic(err) // every value above is a string, int64 or map of them
	}
	return logs
}
