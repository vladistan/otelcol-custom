// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func TestFactoryType(t *testing.T) {
	if got, want := NewFactory().Type(), component.MustNewType(typeStr); got != want {
		t.Fatalf("factory type = %v, want %v", got, want)
	}
}

func TestDefaultConfigNeedsQueueAndRegion(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	if err := cfg.Validate(); err == nil {
		t.Fatal("default config validated without queue_url and region")
	}
	cfg.QueueURL, cfg.Region = "https://sqs.example/q", "eu-central-1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults plus queue_url and region rejected: %v", err)
	}
}

func TestReceiverStartsAndStops(t *testing.T) {
	set := receivertest.NewNopSettings(component.MustNewType(typeStr))
	rcv, err := NewFactory().CreateLogs(context.Background(), set, NewFactory().CreateDefaultConfig(), consumertest.NewNop())
	if err != nil {
		t.Fatalf("create receiver: %v", err)
	}
	if err := rcv.Start(context.Background(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := rcv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
