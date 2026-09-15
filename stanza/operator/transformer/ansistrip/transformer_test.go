// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ansistrip

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no ANSI codes",
			input:    "Hello, World!",
			expected: "Hello, World!",
		},
		{
			name:     "simple color reset",
			input:    "\x1b[0mHello",
			expected: "Hello",
		},
		{
			name:     "red text",
			input:    "\x1b[31mError\x1b[0m",
			expected: "Error",
		},
		{
			name:     "bold green",
			input:    "\x1b[1;32mSuccess\x1b[0m",
			expected: "Success",
		},
		{
			name:     "256 color",
			input:    "\x1b[38;5;208mOrange\x1b[0m",
			expected: "Orange",
		},
		{
			name:     "litellm log format",
			input:    "\x1b[92m23:18:55 - LiteLLM:ERROR\x1b[0m: langsmith.py:392 - Error message",
			expected: "23:18:55 - LiteLLM:ERROR: langsmith.py:392 - Error message",
		},
		{
			name:     "mixed ANSI codes in message",
			input:    "Start \x1b[31mred\x1b[0m middle \x1b[32mgreen\x1b[0m end",
			expected: "Start red middle green end",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only ANSI codes",
			input:    "\x1b[0m\x1b[31m\x1b[0m",
			expected: "",
		},
		{
			name:     "cursor movement codes",
			input:    "\x1b[2J\x1b[HHello",
			expected: "Hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := StripANSI(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestTransformer_ProcessBatch(t *testing.T) {
	cfg := NewConfig()
	cfg.Field = entry.NewBodyField("MESSAGE")
	cfg.OutputIDs = []string{"fake"}

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	fake := testutil.NewFakeOutput(t)
	require.NoError(t, op.SetOutputs([]operator.Operator{fake}))

	transformer, ok := op.(*Transformer)
	require.True(t, ok, "operator should be a *Transformer")

	e1 := entry.New()
	e1.Body = map[string]interface{}{"MESSAGE": "\x1b[31mred\x1b[0m"}
	e2 := entry.New()
	e2.Body = map[string]interface{}{"MESSAGE": "\x1b[32mgreen\x1b[0m"}

	err = transformer.ProcessBatch(context.Background(), []*entry.Entry{e1, e2})
	require.NoError(t, err)

	for _, want := range []string{"red", "green"} {
		select {
		case result := <-fake.Received:
			body, ok := result.Body.(map[string]interface{})
			require.True(t, ok, "body should be a map")
			require.Equal(t, want, body["MESSAGE"])
		default:
			t.Fatal("expected entry from batch was not received")
		}
	}
}
