// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type matcher struct{ patterns []string }

func newMatcher(patterns []string) (*matcher, error) {
	m := &matcher{patterns: make([]string, 0, len(patterns))}
	for _, p := range patterns {
		lowered := strings.ToLower(p)
		if !doublestar.ValidatePattern(lowered) {
			return nil, fmt.Errorf("exclude pattern %q is not a valid glob", p)
		}
		m.patterns = append(m.patterns, lowered)
	}
	return m, nil
}

// excluded matches the root-relative path with "/" separators and no regard to case, as NTFS compares names.
func (m *matcher) excluded(rel string) bool {
	if len(m.patterns) == 0 {
		return false
	}
	s := strings.ToLower(filepath.ToSlash(rel))
	for _, p := range m.patterns {
		if ok, _ := doublestar.Match(p, s); ok {
			return true
		}
	}
	return false
}
