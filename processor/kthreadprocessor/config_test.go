// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCatchAllPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"defaults", defaultPatterns, nil},
		{"star", []string{"*"}, []string{"*"}},
		{"question marks", []string{"kworker*", "??"}, []string{"??"}},
		{"stars and question marks", []string{"*?*"}, []string{"*?*"}},
		{"question mark with text", []string{"migration/?"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Patterns: tt.patterns, AggregateName: defaultAggregateName}

			assert.Equal(t, tt.want, cfg.catchAllPatterns())
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"defaults", Config{Patterns: defaultPatterns, AggregateName: defaultAggregateName}, ""},
		{"one custom pattern", Config{Patterns: []string{"rcu_*"}, AggregateName: "kernel"}, ""},
		{"no patterns", Config{AggregateName: "kernel_threads"}, "patterns must not be empty"},
		{"empty pattern", Config{Patterns: []string{"kworker*", ""}, AggregateName: "kernel_threads"}, "pattern 1 is empty"},
		{"no aggregate name", Config{Patterns: []string{"kworker*"}}, "aggregate_name must be set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
