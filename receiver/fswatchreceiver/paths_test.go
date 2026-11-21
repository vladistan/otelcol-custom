// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"strings"
	"testing"
)

func TestLongPath(t *testing.T) {
	long := `D:\` + strings.Repeat("a", 250)
	uncLong := `\\lab\share\` + strings.Repeat("a", 250)
	tests := []struct {
		name, in, want string
	}{
		{"short path is unchanged", `D:\Projects`, `D:\Projects`},
		{"248 characters is unchanged", `D:\` + strings.Repeat("a", 245), `D:\` + strings.Repeat("a", 245)},
		{"long drive path gets the prefix", long, `\\?\` + long},
		{"long UNC path gets the UNC prefix", uncLong, `\\?\UNC\` + strings.TrimPrefix(uncLong, `\\`)},
		{"already prefixed is unchanged", `\\?\` + long, `\\?\` + long},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := longPath(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsUNC(t *testing.T) {
	for in, want := range map[string]bool{
		`\\lab\share`:   true,
		`//lab/share`:   true,
		`\\?\D:\x`:      false,
		`D:\CDS`:        false,
		`C:\\double`:    false,
		`\single`:       false,
		`\\?\UNC\a\b\c`: true,
	} {
		if got := isUNC(in); got != want {
			t.Errorf("isUNC(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRootKey(t *testing.T) {
	a := rootKey(`D:\Projects`)
	b := rootKey(`d:\projects\`)
	if a != b {
		t.Fatalf("keys differ: %q vs %q", a, b)
	}
	if a != `fswatch/d:\projects` {
		t.Fatalf("got %q", a)
	}
	if c := rootKey(`D:/Projects/`); c != a {
		t.Fatalf("forward slashes give %q, want %q", c, a)
	}
}
