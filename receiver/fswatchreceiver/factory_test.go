// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestFactoryType(t *testing.T) {
	f := NewFactory()
	if f.Type().String() != "fswatch" {
		t.Fatalf("type %q", f.Type())
	}
	if err := componenttest.CheckConfigStruct(f.CreateDefaultConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryRejectsInvalidConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	_, err := createLogsReceiver(t.Context(), receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, &flakySink{})
	if err == nil {
		t.Fatal("a config without paths must fail")
	}
}

func TestFactoryBuildsReceiver(t *testing.T) {
	cfg := validConfig()
	rcv, err := createLogsReceiver(t.Context(), receivertest.NewNopSettings(component.MustNewType(typeStr)), cfg, &flakySink{})
	if err != nil || rcv == nil {
		t.Fatalf("receiver %v err %v", rcv, err)
	}
}
