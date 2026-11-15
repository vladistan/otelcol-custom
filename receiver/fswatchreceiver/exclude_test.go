// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"path/filepath"
	"testing"
)

func TestMatcher(t *testing.T) {
	m, err := newMatcher([]string{`**/~*`, `**/*.tmp`, `**/cache/**`})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		rel  string
		want bool
	}{
		{"~a.tmp", true},
		{"B.TMP", true},
		{"b.Tmp", true},
		{filepath.Join("sub", "deep", "~lock.dx"), true},
		{filepath.Join("sub", "run.dx"), false},
		{"run.dx", false},
		{filepath.Join("a", "cache", "x.bin"), true},
		{filepath.Join("a", "cache"), true},
		{"tmpfile.dx", false},
	}
	for _, tt := range tests {
		if got := m.excluded(tt.rel); got != tt.want {
			t.Errorf("excluded(%q) = %v, want %v", tt.rel, got, tt.want)
		}
	}
}

func TestMatcherEmptyExcludesNothing(t *testing.T) {
	m, err := newMatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.excluded("anything.tmp") {
		t.Fatal("an empty list must exclude nothing")
	}
}

func TestMatcherRejectsBadPattern(t *testing.T) {
	if _, err := newMatcher([]string{`**/[`}); err == nil {
		t.Fatal("want an error for an unterminated class")
	}
}
