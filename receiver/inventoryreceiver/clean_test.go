// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

// cspell:ignore EVNX Liant

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanValue(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"plain":              {"ProLiant DL380", "ProLiant DL380"},
		"trimmed":            {"  Dell Inc.\n", "Dell Inc."},
		"nul padding":        {"ABC123\x00\x00", "ABC123"},
		"control characters": {"AB\x01C\x7f", "ABC"},
		"invalid utf8":       {"AB\xffC", "AB�C"},
		"each bad byte":      {"S\xff\xfe1", "S��1"},
		"oem filler":         {"To Be Filled By O.E.M.", ""},
		"oem filler lower":   {"to be filled by o.e.m.", ""},
		"default string":     {"Default string", ""},
		"not specified":      {"Not Specified", ""},
		"not applicable":     {"Not Applicable", ""},
		"unknown":            {"Unknown", ""},
		"none":               {"None", ""},
		"n/a":                {"N/A", ""},
		"system product":     {"System Product Name", ""},
		"system serial":      {"System Serial Number", ""},
		"base board serial":  {"Base Board Serial Number", ""},
		"zeros":              {"0000000000", ""},
		"ones to nine":       {"0123456789", ""},
		"x fill":             {"XXXXXXXX", ""},
		"dashes":             {"----", ""},
		"no dimm":            {"NO DIMM", ""},
		"empty":              {"", ""},
		"spaces":             {"   ", ""},
		"real serial":        {"S4EVNX0N123456", "S4EVNX0N123456"},
		"serial with zeros":  {"WD-0000A1B2", "WD-0000A1B2"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanValue(tt.in))
		})
	}
}

func TestCleanValueCapsTheLength(t *testing.T) {
	got := cleanValue(strings.Repeat("ab", 500))

	assert.LessOrEqual(t, len(got), maxValueLen)
	assert.NotEmpty(t, got)
}
