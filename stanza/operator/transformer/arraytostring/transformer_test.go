// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package arraytostring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"
)

func TestTransformer_ArrayToString(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		delimiter string
		input     interface{}
		expected  interface{}
	}{
		{
			name:      "array of strings",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{"line1", "line2", "line3"},
			expected:  "line1\nline2\nline3",
		},
		{
			name:      "array with single element",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{"single line"},
			expected:  "single line",
		},
		{
			name:      "empty array",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{},
			expected:  "",
		},
		{
			name:      "string passthrough",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     "already a string",
			expected:  "already a string",
		},
		{
			name:      "custom delimiter",
			field:     "body.MESSAGE",
			delimiter: " | ",
			input:     []interface{}{"part1", "part2"},
			expected:  "part1 | part2",
		},
		{
			name:      "array with byte slices",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{[]byte("bytes1"), []byte("bytes2")},
			expected:  "bytes1\nbytes2",
		},
		{
			name:      "array with mixed types",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{"string", 123, true},
			expected:  "string\n123\ntrue",
		},
		{
			// journald byte array from JSON (bytes come as float64 from JSON unmarshal)
			// This is what journald MESSAGE fields look like when they contain ANSI codes
			// Example: [27,91,57,50,109,72,101,108,108,111] = "\x1b[92mHello"
			name:      "journald byte array (float64 from JSON)",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{float64(72), float64(101), float64(108), float64(108), float64(111)}, // "Hello"
			expected:  "Hello",
		},
		{
			// Real journald byte array with ANSI escape codes (from actual cicd-dkr2 logs)
			// [27,91,57,50,109] = ESC[92m (light green ANSI code)
			name:      "journald byte array with ANSI codes",
			field:     "body.MESSAGE",
			delimiter: "\n",
			input:     []interface{}{float64(27), float64(91), float64(57), float64(50), float64(109), float64(84), float64(101), float64(115), float64(116)},
			expected:  "\x1b[92mTest",
		},
		{
			// Distinguish from mixed content that happens to have numbers > 255
			name:      "array with numbers > 255 treated as strings",
			field:     "body.MESSAGE",
			delimiter: ",",
			input:     []interface{}{float64(100), float64(300), float64(50)}, // 300 > 255, not a byte array
			expected:  "100,300,50",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewConfig()
			cfg.Field = entry.NewBodyField("MESSAGE")
			cfg.Delimiter = tc.delimiter
			cfg.OutputIDs = []string{"fake"}

			set := componenttest.NewNopTelemetrySettings()
			op, err := cfg.Build(set)
			require.NoError(t, err)

			fake := testutil.NewFakeOutput(t)
			require.NoError(t, op.SetOutputs([]operator.Operator{fake}))

			e := entry.New()
			e.Body = map[string]interface{}{
				"MESSAGE": tc.input,
			}

			err = op.Process(context.Background(), e)
			require.NoError(t, err)

			select {
			case result := <-fake.Received:
				body, ok := result.Body.(map[string]interface{})
				require.True(t, ok, "body should be a map")
				require.Equal(t, tc.expected, body["MESSAGE"])
			default:
				t.Fatal("no entry received")
			}
		})
	}
}

func TestTransformer_MissingField(t *testing.T) {
	cfg := NewConfig()
	cfg.Field = entry.NewBodyField("NONEXISTENT")
	cfg.OutputIDs = []string{"fake"}

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	fake := testutil.NewFakeOutput(t)
	require.NoError(t, op.SetOutputs([]operator.Operator{fake}))

	e := entry.New()
	e.Body = map[string]interface{}{
		"MESSAGE": "some value",
	}

	// Should pass through without error when field doesn't exist
	err = op.Process(context.Background(), e)
	require.NoError(t, err)

	select {
	case result := <-fake.Received:
		body, ok := result.Body.(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "some value", body["MESSAGE"])
	default:
		t.Fatal("no entry received")
	}
}

func TestTransformer_ProcessBatch(t *testing.T) {
	cfg := NewConfig()
	cfg.Field = entry.NewBodyField("MESSAGE")
	cfg.Delimiter = "\n"
	cfg.OutputIDs = []string{"fake"}

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	fake := testutil.NewFakeOutput(t)
	require.NoError(t, op.SetOutputs([]operator.Operator{fake}))

	transformer, ok := op.(*Transformer)
	require.True(t, ok, "operator should be a *Transformer")

	e1 := entry.New()
	e1.Body = map[string]interface{}{"MESSAGE": []interface{}{"a", "b"}}
	e2 := entry.New()
	e2.Body = map[string]interface{}{"MESSAGE": []interface{}{"c", "d"}}

	err = transformer.ProcessBatch(context.Background(), []*entry.Entry{e1, e2})
	require.NoError(t, err)

	for _, want := range []string{"a\nb", "c\nd"} {
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

func TestConfig_Build_MissingField(t *testing.T) {
	cfg := NewConfig()
	// Don't set Field - should fail

	set := componenttest.NewNopTelemetrySettings()
	_, err := cfg.Build(set)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing field")
}
