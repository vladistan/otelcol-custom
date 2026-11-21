// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore hwmap hwmapprocessor

package hwmapprocessor

import (
	"errors"
	"strings"
)

// template is a text with {key} placeholders. A placeholder takes the value of the attribute key of
// the point; the text is never run as a command or an expression.
type template struct {
	literal []string // literal[i] comes before keys[i]; the last literal follows the last key
	keys    []string
}

// parseTemplate splits the text at its braces. A brace that is not part of a pair is an error.
func parseTemplate(s string) (template, error) {
	var t template
	rest := s
	for {
		open := strings.IndexByte(rest, '{')
		closing := strings.IndexByte(rest, '}')
		switch {
		case open < 0 && closing < 0:
			t.literal = append(t.literal, rest)
			return t, nil
		case open < 0 || (closing >= 0 && closing < open):
			return template{}, errors.New("a } has no { before it")
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return template{}, errors.New("a { has no }")
		}
		key := rest[open+1 : open+end]
		if key == "" || strings.ContainsAny(key, "{ ") {
			return template{}, errors.New("a placeholder must hold one attribute name")
		}
		t.literal = append(t.literal, rest[:open])
		t.keys = append(t.keys, key)
		rest = rest[open+end+1:]
	}
}

// render fills the placeholders; ok is false when an attribute is missing.
func (t template) render(get func(key string) (string, bool)) (string, bool) {
	var b strings.Builder
	for i, key := range t.keys {
		b.WriteString(t.literal[i])
		v, ok := get(key)
		if !ok {
			return "", false
		}
		b.WriteString(v)
	}
	b.WriteString(t.literal[len(t.literal)-1])
	return b.String(), true
}
