// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestGetSeverityOverride(t *testing.T) {
	tests := []struct {
		name           string
		mnemonic       string
		expectOverride bool
		expectNumber   plog.SeverityNumber
		expectText     string
	}{
		{
			name:           "updown_override",
			mnemonic:       "UPDOWN",
			expectOverride: true,
			expectNumber:   plog.SeverityNumberWarn,
			expectText:     "WARN",
		},
		{
			name:           "portstatus_override",
			mnemonic:       "PORTSTATUS",
			expectOverride: true,
			expectNumber:   plog.SeverityNumberWarn,
			expectText:     "WARN",
		},
		{
			name:           "macflap_override",
			mnemonic:       "MACFLAP_NOTIF",
			expectOverride: true,
			expectNumber:   plog.SeverityNumberWarn,
			expectText:     "WARN",
		},
		{
			name:           "client_associate_info",
			mnemonic:       "CLIENT_ASSOCIATE",
			expectOverride: true,
			expectNumber:   plog.SeverityNumberInfo,
			expectText:     "INFO",
		},
		{
			name:           "no_override",
			mnemonic:       "CLOCKUPDATE",
			expectOverride: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			override := GetSeverityOverride(tt.mnemonic)
			if tt.expectOverride {
				assert.NotNil(t, override)
				assert.Equal(t, tt.expectNumber, override.Number)
				assert.Equal(t, tt.expectText, override.Text)
			} else {
				assert.Nil(t, override)
			}
		})
	}
}

func TestCiscoSeverityToOTEL(t *testing.T) {
	tests := []struct {
		name         string
		severity     string
		expectNumber plog.SeverityNumber
		expectText   string
	}{
		{"emergency", "0", plog.SeverityNumberFatal4, "FATAL"},
		{"alert", "1", plog.SeverityNumberFatal, "FATAL"},
		{"critical", "2", plog.SeverityNumberError2, "ERROR"},
		{"error", "3", plog.SeverityNumberError, "ERROR"},
		{"error_letter", "E", plog.SeverityNumberError, "ERROR"},
		{"warning", "4", plog.SeverityNumberWarn, "WARN"},
		{"warning_letter", "W", plog.SeverityNumberWarn, "WARN"},
		{"notice", "5", plog.SeverityNumberInfo2, "INFO"},
		{"notice_letter", "N", plog.SeverityNumberInfo2, "INFO"},
		{"info", "6", plog.SeverityNumberInfo, "INFO"},
		{"info_letter", "I", plog.SeverityNumberInfo, "INFO"},
		{"debug", "7", plog.SeverityNumberDebug, "DEBUG"},
		{"debug_letter", "D", plog.SeverityNumberDebug, "DEBUG"},
		{"unknown", "X", plog.SeverityNumberUnspecified, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, text := CiscoSeverityToOTEL(tt.severity)
			assert.Equal(t, tt.expectNumber, number)
			assert.Equal(t, tt.expectText, text)
		})
	}
}
