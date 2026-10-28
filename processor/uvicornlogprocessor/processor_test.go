// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package uvicornlogprocessor

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

	p := newProcessor(zap.NewNop(), cfg, sink)

	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", cfg.ServiceName)
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetStr(body)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	return got.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func TestConsumeLogs_AccessLine_EmitsPathAndIntStatus(t *testing.T) {
	cfg := &Config{ServiceName: "uvicorn", ExtractHTTPFields: true}
	line := `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`

	attrs := processOne(t, cfg, line).Attributes()

	path, ok := attrs.Get("url.path")
	if !ok || path.Str() != "/api/healthz/" {
		t.Errorf("url.path = %v (present %v), want /api/healthz/", path.AsString(), ok)
	}
	status, ok := attrs.Get("http.response.status_code")
	if !ok || status.Int() != 200 {
		t.Errorf("http.response.status_code = %v (present %v), want int 200", status.AsString(), ok)
	}
}

func TestConsumeLogs_AccessLine_HasNoUserAgent(t *testing.T) {
	cfg := &Config{ServiceName: "uvicorn", ExtractHTTPFields: true}
	line := `INFO:     10.233.68.104:55370 - "GET /api/healthz/ HTTP/1.1" 200 OK`

	attrs := processOne(t, cfg, line).Attributes()

	if _, ok := attrs.Get("user_agent.original"); ok {
		t.Error("user_agent.original present; uvicorn access lines carry no user agent")
	}
}
