// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Names a status rule uses.
const (
	defaultStateAttribute = "hw.state"
	defaultRawAttribute   = "hw.status.raw"
	sourceValue           = "value"
	sourceAttribute       = "attribute"
	dropState             = "drop"
)

// units are the units a rule may give its points. The scale turns the source unit into one of these.
var units = map[string]bool{
	"Cel": true, "W": true, "V": true, "A": true, "By": true, "1": true, "rpm": true, "s": true, "ms": true, "Hz": true,
	"{error}": true, "{packet}": true,
	"By/s": true,
}

// Config lists the mapping rules. The processor does nothing until a pipeline lists it.
type Config struct {
	Rules []Rule `mapstructure:"rules"`
}

// Rule maps the points of one metric to a target metric.
type Rule struct {
	// Name identifies the rule in errors; it must be unique.
	Name string `mapstructure:"name"`
	// Match selects the points.
	Match Match `mapstructure:"match"`
	// Target is the name of the metric the points leave as.
	Target string `mapstructure:"target"`
	// Unit is the unit of the target metric; empty keeps the unit of the source.
	Unit string `mapstructure:"unit"`
	// Scale multiplies the value; it must be above 0. Unset means 1.
	Scale *float64 `mapstructure:"scale"`
	// DropWhenValue drops a point whose value equals it.
	DropWhenValue *float64 `mapstructure:"drop_when_value"`
	// Labels sets point attributes (hw.type, hw.id, hw.name) from templates: a text with {key}
	// placeholders that take the value of the source attribute key.
	Labels map[string]string `mapstructure:"labels"`
	// IndexNames sets hw.id and hw.name from a static map of an index attribute.
	IndexNames *IndexNames `mapstructure:"index_names"`
	// Constants sets fixed point attributes.
	Constants map[string]string `mapstructure:"constants"`
	// Resource sets fixed resource attributes (hw.vendor, hw.model).
	Resource map[string]string `mapstructure:"resource"`
	// Status turns a value or an attribute into hw.status points.
	Status *Status `mapstructure:"status"`
	// EnumGauge turns a value or an attribute into one 1/0 gauge point; it excludes Status.
	EnumGauge *EnumGauge `mapstructure:"enum_gauge"`
}

// Match selects points by metric name and attributes.
type Match struct {
	// Metric is the source metric name.
	Metric string `mapstructure:"metric"`
	// Attributes must be present on the point; a non-empty value must also equal the attribute.
	Attributes map[string]string `mapstructure:"attributes"`
}

// IndexNames gives hw.id the index and hw.name the name of that index.
type IndexNames struct {
	Attribute string            `mapstructure:"attribute"`
	Names     map[string]string `mapstructure:"names"`
}

// Status describes how a raw state becomes hw.status points.
type Status struct {
	// Source is "value" (the number of the point, the default) or "attribute".
	Source string `mapstructure:"source"`
	// Attribute holds the raw state when Source is "attribute".
	Attribute string `mapstructure:"attribute"`
	// States lists the states one point is emitted for; default ok, degraded, failed.
	States []string `mapstructure:"states"`
	// Map turns a raw state into one of States, or "drop" for no point.
	Map map[string]string `mapstructure:"map"`
	// DefaultState is taken by a raw state that is not in Map.
	DefaultState string `mapstructure:"default_state"`
	// StateAttribute and RawAttribute name the attributes of the state and of the raw text.
	StateAttribute string `mapstructure:"state_attribute"`
	RawAttribute   string `mapstructure:"raw_attribute"`
}

// EnumGauge describes how a raw state becomes one gauge point that is 1 or 0.
type EnumGauge struct {
	// Source is "value" (the number of the point, the default) or "attribute".
	Source string `mapstructure:"source"`
	// Attribute holds the raw state when Source is "attribute".
	Attribute string `mapstructure:"attribute"`
	// On lists the raw states that give 1; every other state gives 0.
	On []string `mapstructure:"on"`
	// Names turns a raw state into the text kept in the state attribute. With names, a raw state that
	// is not listed is kept as read, counted and logged once.
	Names map[string]string `mapstructure:"names"`
	// StateAttribute names the attribute that keeps the state on every point.
	StateAttribute string `mapstructure:"state_attribute"`
}

func (g *EnumGauge) stateAttribute() string {
	if g.StateAttribute == "" {
		return defaultStateAttribute
	}
	return g.StateAttribute
}

func (g *EnumGauge) source() string {
	if g.Source == "" {
		return sourceValue
	}
	return g.Source
}

func (g *EnumGauge) validate() error {
	switch g.source() {
	case sourceValue:
	case sourceAttribute:
		if g.Attribute == "" {
			return errors.New("enum_gauge.attribute is missing")
		}
	default:
		return fmt.Errorf("enum_gauge.source %q is not value or attribute", g.Source)
	}
	if len(g.On) == 0 {
		return errors.New("enum_gauge.on is empty")
	}
	for raw := range g.Names {
		if raw == "" {
			return errors.New("enum_gauge.names has an empty key")
		}
	}
	for _, raw := range g.On {
		if raw == "" {
			return errors.New("enum_gauge.on has an empty entry")
		}
		if _, listed := g.Names[raw]; len(g.Names) > 0 && !listed {
			return fmt.Errorf("enum_gauge.on state %q is not in names", raw)
		}
	}
	return nil
}

func (s *Status) states() []string {
	if len(s.States) == 0 {
		return []string{"ok", "degraded", "failed"}
	}
	return s.States
}

func (s *Status) stateAttribute() string {
	if s.StateAttribute == "" {
		return defaultStateAttribute
	}
	return s.StateAttribute
}

func (s *Status) rawAttribute() string {
	if s.RawAttribute == "" {
		return defaultRawAttribute
	}
	return s.RawAttribute
}

func (s *Status) source() string {
	if s.Source == "" {
		return sourceValue
	}
	return s.Source
}

// Validate checks every rule and names the rule that fails.
func (c *Config) Validate() error {
	names := make(map[string]bool, len(c.Rules))
	matches := make(map[string]string, len(c.Rules))
	var errs []error
	for i := range c.Rules {
		r := &c.Rules[i]
		if err := r.validate(); err != nil {
			label := r.Name
			if label == "" {
				label = fmt.Sprintf("#%d", i+1)
			}
			errs = append(errs, fmt.Errorf("rule %q: %w", label, err))
			continue
		}
		if names[r.Name] {
			errs = append(errs, fmt.Errorf("rule %q: the name is used twice", r.Name))
		}
		names[r.Name] = true
		key := r.matchKey()
		if other, dup := matches[key]; dup {
			errs = append(errs, fmt.Errorf("rule %q: same match as rule %q", r.Name, other))
		}
		matches[key] = r.Name
	}
	return errors.Join(errs...)
}

// matchKey is the metric and the sorted attribute conditions of a rule.
func (r *Rule) matchKey() string {
	pairs := make([]string, 0, len(r.Match.Attributes))
	for k, v := range r.Match.Attributes {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return r.Match.Metric + "|" + strings.Join(pairs, ",")
}

func (r *Rule) validate() error {
	switch {
	case r.Name == "":
		return errors.New("name is missing")
	case r.Match.Metric == "":
		return errors.New("match.metric is missing")
	case r.Target == "":
		return errors.New("target is missing")
	case r.Unit != "" && !units[r.Unit]:
		return fmt.Errorf("unknown unit %q", r.Unit)
	case r.Scale != nil && !(*r.Scale > 0 && !math.IsInf(*r.Scale, 1)):
		return fmt.Errorf("scale must be a finite number above 0, got %v", *r.Scale)
	}
	if err := validateTemplates(r.Labels, "labels"); err != nil {
		return err
	}
	if err := validateKeys(r.Constants, "constants"); err != nil {
		return err
	}
	if err := validateKeys(r.Resource, "resource"); err != nil {
		return err
	}
	if r.IndexNames != nil && r.IndexNames.Attribute == "" {
		return errors.New("index_names.attribute is missing")
	}
	if r.Status != nil && r.EnumGauge != nil {
		return errors.New("status and enum_gauge cannot both be set")
	}
	if r.EnumGauge != nil {
		return r.EnumGauge.validate()
	}
	if r.Status != nil {
		return r.Status.validate()
	}
	return nil
}

// validateKeys refuses an empty key in a map of fixed attributes.
func validateKeys(m map[string]string, field string) error {
	for k := range m {
		if k == "" {
			return fmt.Errorf("%s has an empty key", field)
		}
	}
	return nil
}

func validateTemplates(m map[string]string, field string) error {
	for k, v := range m {
		if k == "" {
			return fmt.Errorf("%s has an empty key", field)
		}
		if _, err := parseTemplate(v); err != nil {
			return fmt.Errorf("%s[%s]: %w", field, k, err)
		}
	}
	return nil
}

func (s *Status) validate() error {
	switch s.source() {
	case sourceValue:
	case sourceAttribute:
		if s.Attribute == "" {
			return errors.New("status.attribute is missing")
		}
	default:
		return fmt.Errorf("status.source %q is not value or attribute", s.Source)
	}
	listed := map[string]bool{}
	for _, st := range s.states() {
		if st == "" || st == dropState {
			return fmt.Errorf("status.states has the unusable entry %q", st)
		}
		if listed[st] {
			return fmt.Errorf("status.states lists %q twice", st)
		}
		listed[st] = true
	}
	for raw, st := range s.Map {
		if st != dropState && !listed[st] {
			return fmt.Errorf("status.map[%s] is %q, which is not in states", raw, st)
		}
	}
	if s.DefaultState != "" && !listed[s.DefaultState] {
		return fmt.Errorf("status.default_state %q is not in states", s.DefaultState)
	}
	return nil
}
