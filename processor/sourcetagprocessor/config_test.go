// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"strings"
	"testing"
)

func TestValidate_AcceptsEachProvider(t *testing.T) {
	for _, provider := range []string{"aws", "gcp", "azure"} {
		t.Run(provider, func(t *testing.T) {
			if err := (&Config{Provider: provider}).Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidate_RejectsOtherProviders(t *testing.T) {
	for name, provider := range map[string]string{"empty": "", "unknown": "oracle", "wrong case": "AWS"} {
		t.Run(name, func(t *testing.T) {
			err := (&Config{Provider: provider}).Validate()

			if err == nil || !strings.Contains(err.Error(), "provider") {
				t.Errorf("Validate() = %v, want an error naming provider", err)
			}
		})
	}
}

func TestDefaultConfig_HasNoProvider(t *testing.T) {
	if got := NewFactory().CreateDefaultConfig().(*Config).Provider; got != "" {
		t.Errorf("default provider = %q, want empty so a missing setting fails validation", got)
	}
}
