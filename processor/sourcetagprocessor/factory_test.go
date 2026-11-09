// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestFactoryType(t *testing.T) {
	if got, want := NewFactory().Type(), component.MustNewType(typeStr); got != want {
		t.Fatalf("factory type = %v, want %v", got, want)
	}
}

func TestFactoryTypeName(t *testing.T) {
	if got := NewFactory().Type().String(); got != "sourcetag" {
		t.Fatalf("factory type = %q, want sourcetag", got)
	}
}

func TestCreateLogs_InvalidConfigReturnsValidationError(t *testing.T) {
	cfg := &Config{Provider: "oracle"}
	set := processortest.NewNopSettings(component.MustNewType(typeStr))

	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink))

	if want := cfg.Validate(); err == nil || err.Error() != want.Error() {
		t.Errorf("CreateLogs error = %v, want the Validate error %v", err, want)
	}
	if proc != nil {
		t.Error("CreateLogs returned a processor for an invalid config")
	}
}

func TestRulesFor_UnknownProviderIsAnError(t *testing.T) {
	if _, err := rulesFor("oracle"); err == nil {
		t.Error("rulesFor accepted an unknown provider")
	}
	for _, p := range []string{providerAWS, providerGCP, providerAzure} {
		if _, err := rulesFor(p); err != nil {
			t.Errorf("rulesFor(%q) = %v", p, err)
		}
	}
}
