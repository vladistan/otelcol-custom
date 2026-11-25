// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIsGetPrinterAttributesWithTheSevenAttributes(t *testing.T) {
	b := encodeGetPrinterAttributes("ipp://printer.example:631/ipp/print", 7)

	// version 1.1, operation 0x000B (Get-Printer-Attributes), request id 7
	assert.Equal(t, []byte{1, 1, 0x00, 0x0B, 0, 0, 0, 7, 0x01}, b[:9])
	assert.Equal(t, byte(0x03), b[len(b)-1])

	m, err := decodeMessage(b)
	require.NoError(t, err)
	assert.Equal(t, uint16(0x000B), m.code)
	assert.Equal(t, []string{
		"printer-state", "printer-state-reasons", "printer-is-accepting-jobs",
		"queued-job-count", "marker-levels", "marker-names", "marker-types",
	}, requestedNames(m))
}

func TestRequestCarriesCharsetLanguageAndURI(t *testing.T) {
	m, err := decodeMessage(encodeGetPrinterAttributes("ipp://printer.example/ipp/print", 1))
	require.NoError(t, err)

	attrs := map[string]string{}
	for _, a := range m.groups[0].attrs {
		attrs[a.name] = string(a.values[0].data)
	}
	assert.Equal(t, "utf-8", attrs["attributes-charset"])
	assert.Equal(t, "en", attrs["attributes-natural-language"])
	assert.Equal(t, "ipp://printer.example/ipp/print", attrs["printer-uri"])
}

func TestDecodeReadsPrinterAttributesAndAdditionalValues(t *testing.T) {
	m, err := decodeMessage(encodeResponse(0, healthyAttrs()...))
	require.NoError(t, err)

	attrs, ok := m.printerAttributes()
	require.True(t, ok)
	state, _ := attrs["printer-state"][0].asInt()
	assert.Equal(t, int32(3), state)
	assert.Len(t, attrs["marker-levels"], 4)
	accepting, _ := attrs["printer-is-accepting-jobs"][0].asBool()
	assert.True(t, accepting)
	assert.Equal(t, "Cyan", attrs["marker-names"][1].asString())
}

func TestDecodeKeepsTheStatusCode(t *testing.T) {
	m, err := decodeMessage(encodeResponse(0x0400))

	require.NoError(t, err)
	assert.Equal(t, uint16(0x0400), m.code)
}

func TestDecodeRejectsDamagedMessages(t *testing.T) {
	good := encodeResponse(0, healthyAttrs()...)
	tests := map[string][]byte{
		"empty":                  nil,
		"header only":            good[:5],
		"cut inside a value":     good[:len(good)-12],
		"no end tag":             good[:len(good)-1],
		"value before a group":   {1, 1, 0, 0, 0, 0, 0, 1, 0x44, 0, 1, 'a', 0, 1, 'b', 0x03},
		"additional value first": {1, 1, 0, 0, 0, 0, 0, 1, 0x04, 0x44, 0, 0, 0, 1, 'b', 0x03},
	}
	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeMessage(b)

			assert.Error(t, err)
		})
	}
}

func TestDecodeAcceptsAnOutOfBandValue(t *testing.T) {
	attrs := []wireAttr{{tag: 0x13, name: "marker-levels", values: [][]byte{{}}}}

	m, err := decodeMessage(encodeResponse(0, attrs...))

	require.NoError(t, err)
	got, _ := m.printerAttributes()
	_, isInt := got["marker-levels"][0].asInt()
	assert.False(t, isInt)
}

func TestHTTPURLMapsTheScheme(t *testing.T) {
	tests := map[string]string{
		"ipp://printer.example:631/ipp/print":   "http://printer.example:631/ipp/print",
		"ipp://printer.example/ipp/print":       "http://printer.example:631/ipp/print",
		"ipps://printer.example/ipp/print":      "https://printer.example:631/ipp/print",
		"ipps://printer.example:8631/ipp/print": "https://printer.example:8631/ipp/print",
	}
	for in, want := range tests {
		got, err := httpURL(in)

		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}
