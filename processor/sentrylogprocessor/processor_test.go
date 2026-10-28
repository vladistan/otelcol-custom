// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sentrylogprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

func processOne(t *testing.T, cfg *Config, body string) plog.LogRecord {
	t.Helper()

	var got plog.Logs
	sink, err := consumer.NewLogs(func(_ context.Context, ld plog.Logs) error {
		got = ld
		return nil
	})
	if err != nil {
		t.Fatalf("NewLogs: %v", err)
	}

	p := &logsProcessor{config: cfg, logger: zap.NewNop(), nextConsumer: sink}

	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr(body)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	return got.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func TestConsumeLogs_AccessLine_EmitsProbeAttributes(t *testing.T) {
	line := "02:30:21 [INFO] sentry.access.api: api.access " +
		"(method='GET' view='sentry.web.frontend.home.HomeView' response='302' " +
		"path='/api/0/projects/' user_agent='ELB-HealthChecker/2.0')"

	attrs := processOne(t, &Config{ExtractHTTPFields: true}, line).Attributes()

	want := map[string]string{
		"url.path":                  "/api/0/projects/",
		"user_agent.original":       "ELB-HealthChecker/2.0",
		"http.response.status_code": "302",
	}
	for key, value := range want {
		got, ok := attrs.Get(key)
		if !ok {
			t.Fatalf("attribute %q missing", key)
		}
		if got.Str() != value {
			t.Errorf("attribute %q = %q, want %q", key, got.Str(), value)
		}
	}
}

func TestConsumeLogs_HTTPFieldsDisabled_OmitsProbeAttributes(t *testing.T) {
	line := "02:30:21 [INFO] sentry.access.api: api.access " +
		"(method='GET' response='302' path='/x' user_agent='ua')"

	attrs := processOne(t, &Config{ExtractHTTPFields: false}, line).Attributes()

	for _, key := range []string{"url.path", "user_agent.original", "http.response.status_code"} {
		if _, ok := attrs.Get(key); ok {
			t.Errorf("attribute %q present with extract_http_fields off", key)
		}
	}
}
