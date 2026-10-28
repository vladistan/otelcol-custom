// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package probesample

import "testing"

func TestConfigValidates(t *testing.T) {
	if err := (&Config{}).Validate(); err != nil {
		t.Fatalf("empty config does not validate: %v", err)
	}
}
