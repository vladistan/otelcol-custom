// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

// goodRule is a rule that passes Validate; tests change one thing of it.
func goodRule(name string) Rule {
	return Rule{
		Name:   name,
		Match:  Match{Metric: "test.temperature.milli"},
		Target: "hw.temperature",
		Unit:   "Cel",
		Scale:  f64(0.001),
	}
}

func TestValidate_AGoodConfigPasses(t *testing.T) {
	cfg := &Config{Rules: []Rule{goodRule("a"), goodRule("b")}}
	cfg.Rules[1].Match.Metric = "test.other"

	assert.NoError(t, cfg.Validate())
}

// A name used twice and a match used twice are two separate refusals with their own message.
func TestValidate_DuplicateNameAndDuplicateMatchAreSeparateRefusals(t *testing.T) {
	sameName := goodRule("a")
	other := goodRule("a")
	other.Match.Metric = "test.other"

	err := (&Config{Rules: []Rule{sameName, other}}).Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), `rule "a": the name is used twice`)
	assert.NotContains(t, err.Error(), "same match")

	sameMatch := goodRule("b")

	err = (&Config{Rules: []Rule{goodRule("a"), sameMatch}}).Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), `rule "b": same match as rule "a"`)
	assert.NotContains(t, err.Error(), "used twice")
}

func TestValidate_NoRulesIsFine(t *testing.T) {
	assert.NoError(t, (&Config{}).Validate())
}

func TestValidate_RefusesAndNamesTheRule(t *testing.T) {
	cases := map[string]func(*Config){
		"duplicate rule name": func(c *Config) { c.Rules = append(c.Rules, goodRule("a")) },
		"duplicate match": func(c *Config) {
			r := goodRule("b")
			c.Rules = append(c.Rules, r)
		},
		"missing name":        func(c *Config) { c.Rules[0].Name = "" },
		"missing metric":      func(c *Config) { c.Rules[0].Match.Metric = "" },
		"missing target":      func(c *Config) { c.Rules[0].Target = "" },
		"unknown unit":        func(c *Config) { c.Rules[0].Unit = "furlongs" },
		"zero scale":          func(c *Config) { c.Rules[0].Scale = f64(0) },
		"negative scale":      func(c *Config) { c.Rules[0].Scale = f64(-1) },
		"unbalanced template": func(c *Config) { c.Rules[0].Labels = map[string]string{"hw.name": "{open"} },
		"empty label key":     func(c *Config) { c.Rules[0].Labels = map[string]string{"": "x"} },
	}
	for name, mutate := range cases {
		cfg := &Config{Rules: []Rule{goodRule("a")}}
		mutate(cfg)

		err := cfg.Validate()

		require.Error(t, err, name)
		want := `"a"`
		if name == "missing name" {
			want = `"#1"`
		}
		assert.Contains(t, err.Error(), want, name)
	}
}

func TestValidate_UnitOfASpecialRuleIsOptional(t *testing.T) {
	r := goodRule("a")
	r.Unit = ""
	r.Scale = nil

	assert.NoError(t, (&Config{Rules: []Rule{r}}).Validate())
}

func statusRule() Rule {
	return Rule{
		Name: "state", Match: Match{Metric: "test.state"}, Target: "hw.status", Unit: "1",
		Status: &Status{States: []string{"ok", "degraded", "failed"}, Map: map[string]string{"1": "ok", "2": "degraded", "3": "failed", "5": "drop"}},
	}
}

func TestValidate_StatusRule(t *testing.T) {
	assert.NoError(t, (&Config{Rules: []Rule{statusRule()}}).Validate())

	cases := map[string]func(*Status){
		"map to an unlisted state":   func(s *Status) { s.Map["1"] = "great" },
		"default state not listed":   func(s *Status) { s.DefaultState = "great" },
		"attribute source no key":    func(s *Status) { s.Source = "attribute" },
		"unknown source":             func(s *Status) { s.Source = "magic" },
		"empty state list entry":     func(s *Status) { s.States = []string{"ok", ""} },
		"duplicate state list entry": func(s *Status) { s.States = []string{"ok", "ok"} },
	}
	for name, mutate := range cases {
		r := statusRule()
		mutate(r.Status)

		err := (&Config{Rules: []Rule{r}}).Validate()

		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `"state"`, name)
	}
}

func TestValidate_IndexNames(t *testing.T) {
	r := goodRule("a")
	r.IndexNames = &IndexNames{Attribute: "idx", Names: map[string]string{"1": "PP"}}
	assert.NoError(t, (&Config{Rules: []Rule{r}}).Validate())

	r.IndexNames = &IndexNames{Names: map[string]string{"1": "PP"}}
	assert.Error(t, (&Config{Rules: []Rule{r}}).Validate(), "an index map needs the attribute that holds the index")
}

func TestValidate_ManyRulesStaysFast(t *testing.T) {
	cfg := &Config{}
	for i := range 5000 {
		r := goodRule("rule-" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)))
		r.Match.Metric = "m." + r.Name
		cfg.Rules = append(cfg.Rules, r)
	}

	assert.NoError(t, cfg.Validate())
}
