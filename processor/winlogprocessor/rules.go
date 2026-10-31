// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	logRecordOriginalKey = "log.record.original"
	maxLevel             = 5
)

// DefaultDropRules is the list of routine Security audit events, the
// SystemDataArchiver Information and Verbose events, and the collector's own
// Information events. 4624 stays collected: a rule cannot tell service logons
// from interactive ones.
func DefaultDropRules() []DropRule {
	return []DropRule{
		{
			Name:     "security-audit-noise",
			Channels: []string{"Security"},
			EventIDs: []string{"4634", "4672", "5379"},
		},
		{
			Name:     "system-data-archiver-noise",
			Channels: []string{"Microsoft-Windows-SystemDataArchiver/Diagnostic"},
			EventIDs: []string{"2049", "2050"},
			Levels:   []int{4, 5},
		},
		{
			Name:      "collector-self-info",
			Levels:    []int{4},
			Providers: []string{"otelcol-edge"},
		},
	}
}

type idRange struct{ lo, hi uint32 }

type rule struct {
	name      string
	channels  []string
	ids       []idRange
	levels    uint8 // bit n set = level n; 0 = any
	providers []string
	fields    []fieldCondition
	tallyBy   []string
}

type fieldCondition struct {
	name     string
	equals   []string
	endsWith string
	isSuffix bool
}

type ruleSet struct {
	rules         []rule
	needsID       bool
	needsProvider bool
}

// eventFields are the values the rules read from one record.
type eventFields struct {
	ok       bool
	rec      plog.LogRecord
	channel  string
	id       uint32
	hasID    bool
	provider string
	level    int
	levelSet bool
}

func compileRules(rules []DropRule) (ruleSet, error) {
	var set ruleSet
	seen := make(map[string]struct{}, len(rules))
	for i, dr := range rules {
		if dr.Name == "" {
			return ruleSet{}, fmt.Errorf("drop_rules[%d]: name is empty", i)
		}
		if _, dup := seen[dr.Name]; dup {
			return ruleSet{}, fmt.Errorf("drop_rules[%d]: duplicate rule name %q", i, dr.Name)
		}
		seen[dr.Name] = struct{}{}
		if len(dr.Channels) == 0 && len(dr.EventIDs) == 0 && len(dr.Levels) == 0 &&
			len(dr.Providers) == 0 && len(dr.Fields) == 0 {
			return ruleSet{}, fmt.Errorf("drop_rules[%d] %q: rule sets no field", i, dr.Name)
		}
		r, err := compileRule(dr)
		if err != nil {
			return ruleSet{}, fmt.Errorf("drop_rules[%d] %q: %w", i, dr.Name, err)
		}
		set.rules = append(set.rules, r)
		set.needsID = set.needsID || len(r.ids) > 0
		set.needsProvider = set.needsProvider || len(r.providers) > 0
	}
	return set, nil
}

func compileRule(dr DropRule) (rule, error) {
	r := rule{name: dr.Name}
	for _, c := range dr.Channels {
		if c == "" {
			return rule{}, fmt.Errorf("channel is empty")
		}
		r.channels = append(r.channels, c)
	}
	for _, p := range dr.Providers {
		if p == "" {
			return rule{}, fmt.Errorf("provider is empty")
		}
		r.providers = append(r.providers, p)
	}
	for _, l := range dr.Levels {
		if l < 0 || l > maxLevel {
			return rule{}, fmt.Errorf("level %d is outside 0-%d", l, maxLevel)
		}
		r.levels |= 1 << uint(l)
	}
	for _, s := range dr.EventIDs {
		rng, err := parseIDRange(s)
		if err != nil {
			return rule{}, err
		}
		r.ids = append(r.ids, rng)
	}
	for _, fc := range dr.Fields {
		cond, err := compileField(fc)
		if err != nil {
			return rule{}, err
		}
		r.fields = append(r.fields, cond)
	}
	tally, err := compileTallyBy(dr.TallyBy)
	if err != nil {
		return rule{}, err
	}
	r.tallyBy = tally
	return r, nil
}

func compileField(fc FieldCondition) (fieldCondition, error) {
	if fc.Name == "" {
		return fieldCondition{}, fmt.Errorf("field name is empty")
	}
	switch {
	case len(fc.Equals) > 0 && fc.EndsWith != "":
		return fieldCondition{}, fmt.Errorf("field %q sets both equals and ends_with", fc.Name)
	case len(fc.Equals) == 0 && fc.EndsWith == "":
		return fieldCondition{}, fmt.Errorf("field %q sets neither equals nor ends_with", fc.Name)
	}
	for _, v := range fc.Equals {
		if v == "" {
			return fieldCondition{}, fmt.Errorf("field %q equals has an empty value", fc.Name)
		}
	}
	return fieldCondition{
		name: fc.Name, equals: slices.Clone(fc.Equals), endsWith: fc.EndsWith, isSuffix: fc.EndsWith != "",
	}, nil
}

func compileTallyBy(names []string) ([]string, error) {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return nil, fmt.Errorf("tally_by field name is empty")
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("tally_by lists %s twice", name)
		}
		seen[name] = struct{}{}
	}
	return slices.Clone(names), nil
}

func parseIDRange(s string) (idRange, error) {
	loText, hiText, isRange := strings.Cut(s, "-")
	lo, err := strconv.ParseUint(strings.TrimSpace(loText), 10, 32)
	if err != nil {
		return idRange{}, fmt.Errorf("invalid event id %q", s)
	}
	if !isRange {
		return idRange{lo: uint32(lo), hi: uint32(lo)}, nil
	}
	hi, err := strconv.ParseUint(strings.TrimSpace(hiText), 10, 32)
	if err != nil {
		return idRange{}, fmt.Errorf("invalid event id %q", s)
	}
	if lo > hi {
		return idRange{}, fmt.Errorf("event id range %q: lo %d is greater than hi %d", s, lo, hi)
	}
	return idRange{lo: uint32(lo), hi: uint32(hi)}, nil
}

// extract reads the channel, and when some rule needs them the event ID and
// provider. ok is false for a record whose body is not a map with a channel.
func (s *ruleSet) extract(rec plog.LogRecord) eventFields {
	f := eventFields{rec: rec}
	if rec.Body().Type() != pcommon.ValueTypeMap {
		return f
	}
	body := rec.Body().Map()
	ch, found := body.Get("channel")
	if !found || ch.Type() != pcommon.ValueTypeStr || ch.Str() == "" {
		return f
	}
	f.ok = true
	f.channel = ch.Str()
	if s.needsID {
		if m, ok := body.Get("event_id"); ok && m.Type() == pcommon.ValueTypeMap {
			if id, ok := m.Map().Get("id"); ok && id.Type() == pcommon.ValueTypeInt && id.Int() >= 0 {
				f.id, f.hasID = uint32(id.Int()), true
			}
		}
	}
	if s.needsProvider {
		if m, ok := body.Get("provider"); ok && m.Type() == pcommon.ValueTypeMap {
			if name, ok := m.Map().Get("name"); ok && name.Type() == pcommon.ValueTypeStr {
				f.provider = name.Str()
			}
		}
	}
	return f
}

// firstMatch returns the index of the first rule that matches, or -1.
func (s *ruleSet) firstMatch(f *eventFields) int {
	for i := range s.rules {
		if s.rules[i].matches(f) {
			return i
		}
	}
	return -1
}

func (r *rule) matches(f *eventFields) bool {
	if len(r.channels) > 0 && !containsFold(r.channels, f.channel) {
		return false
	}
	if len(r.ids) > 0 && (!f.hasID || !inRanges(r.ids, f.id)) {
		return false
	}
	if len(r.providers) > 0 && !containsFold(r.providers, f.provider) {
		return false
	}
	if r.levels != 0 {
		lvl := f.eventLevel()
		if lvl < 0 || r.levels&(1<<uint(lvl)) == 0 {
			return false
		}
	}
	for i := range r.fields {
		if !r.fields[i].matches(f) {
			return false
		}
	}
	return true
}

func (c *fieldCondition) matches(f *eventFields) bool {
	value, ok := f.eventDataValue(c.name)
	if !ok {
		return false
	}
	if c.isSuffix {
		return strings.HasSuffix(value, c.endsWith)
	}
	return slices.Contains(c.equals, value)
}

// eventDataValue reads one event data field in either receiver format.
func (f *eventFields) eventDataValue(name string) (string, bool) {
	if f.rec.Body().Type() != pcommon.ValueTypeMap {
		return "", false
	}
	eventData, ok := nestedMap(f.rec.Body().Map(), "event_data")
	if !ok {
		return "", false
	}
	raw, hasData := eventData.Get("data")
	if !hasData || raw.Type() != pcommon.ValueTypeSlice {
		if v, ok := eventData.Get(name); ok {
			return v.AsString(), true
		}
		return "", false
	}
	for i := 0; i < raw.Slice().Len(); i++ {
		if raw.Slice().At(i).Type() != pcommon.ValueTypeMap {
			continue
		}
		if v, ok := raw.Slice().At(i).Map().Get(name); ok {
			return v.AsString(), true
		}
	}
	return "", false
}

func containsFold(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

func inRanges(ranges []idRange, id uint32) bool {
	for _, r := range ranges {
		if id >= r.lo && id <= r.hi {
			return true
		}
	}
	return false
}

// eventLevel reads the numeric level once, from <Level> in the event XML, and
// falls back to the rendered English level name. It returns -1 when unknown.
func (f *eventFields) eventLevel() int {
	if f.levelSet {
		return f.level
	}
	f.levelSet = true
	f.level = -1
	if orig, ok := f.rec.Attributes().Get(logRecordOriginalKey); ok && orig.Type() == pcommon.ValueTypeStr {
		if n, ok := xmlLevel(orig.Str()); ok {
			f.level = n
			return n
		}
	}
	if f.rec.Body().Type() == pcommon.ValueTypeMap {
		if v, ok := f.rec.Body().Map().Get("level"); ok && v.Type() == pcommon.ValueTypeStr {
			f.level = renderedLevel(v.Str())
		}
	}
	return f.level
}

// xmlLevel scans for the first <Level>n</Level>, which is the System level.
func xmlLevel(xml string) (int, bool) {
	const open, closing = "<Level>", "</Level>"
	start := strings.Index(xml, open)
	if start < 0 {
		return 0, false
	}
	rest := xml[start+len(open):]
	end := strings.Index(rest, closing)
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || n < 0 || n > maxLevel {
		return 0, false
	}
	return n, true
}

// renderedLevel maps a level name, or a raw digit such as "5", to a level.
func renderedLevel(name string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(name)); err == nil {
		if n < 0 || n > maxLevel {
			return -1
		}
		return n
	}
	switch {
	case strings.EqualFold(name, "Critical"):
		return 1
	case strings.EqualFold(name, "Error"):
		return 2
	case strings.EqualFold(name, "Warning"):
		return 3
	case strings.EqualFold(name, "Information"):
		return 4
	case strings.EqualFold(name, "Verbose"):
		return 5
	}
	return -1
}
