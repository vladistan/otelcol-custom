// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// scopesOf sends the bodies as one batch under one scope and returns the scope name of each
// record, keyed by its shaped body.
func scopesOf(t *testing.T, bodies ...string) map[string]string {
	t.Helper()
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	sl.Scope().SetName("receiver")
	for _, b := range bodies {
		sl.LogRecords().AppendEmpty().Body().SetStr(b)
	}
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	out := h.sink.AllLogs()[0]
	for i := range out.ResourceLogs().Len() {
		scopes := out.ResourceLogs().At(i).ScopeLogs()
		for j := range scopes.Len() {
			records := scopes.At(j).LogRecords()
			for k := range records.Len() {
				got[records.At(k).Body().Str()] = scopes.At(j).Scope().Name()
			}
		}
	}
	return got
}

func TestScope_TwoRecordsOfOneBatch_KeepTheirOwnScopeName(t *testing.T) {
	otherComponent := zapLine("2026-11-06T21:12:29.300Z", "info", "builders/builders.go:28", "Development component.", `{"otelcol.component.id": "identityfill"}`)
	cases := map[string]struct {
		first, second string
		want          map[string]string
	}{
		"collector": {collectorStandard, otherComponent, map[string]string{"Starting HTTP server": "otlp", "Development component.": "identityfill"}},
		"tracing": {tracingInfo, `{"timestamp":"2026-11-19T22:41:05Z","level":"INFO","fields":{"message":"Flushed"},"target":"a::b"}`,
			map[string]string{"Inserted 2 rows": "rust_consumer::strategies::writer", "Flushed": "a::b"}},
		"pino": {pinoError, `[API] {"level":30,"time":1795129202193,"msg":"request done"}`,
			map[string]string{"fetch failed": "JOB-RUNNER", "request done": "API"}},
		"mongod": {mongodEnded, mongodAuth,
			map[string]string{"Connection ended remote=192.0.2.10:53714 connectionCount=23": "NETWORK", "Authentication succeeded principal=__system remote=192.0.2.10:53720": "ACCESS"}},
	}
	for name, c := range cases {
		got := scopesOf(t, c.first, c.second)

		if len(got) != len(c.want) {
			t.Errorf("%s: scopes = %v, want %v", name, got, c.want)
			continue
		}
		for body, scope := range c.want {
			if got[body] != scope {
				t.Errorf("%s: record %q has scope %q, want %q", name, body, got[body], scope)
			}
		}
	}
}
