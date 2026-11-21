// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import "testing"

// Invented fixtures of slog and zap JSON lines (shapes of real lines, made-up values). The logrus
// branch writes these formats; no test here runs them through it, so they only check that they
// parse as JSON objects with a level and a msg.
const (
	slogError  = `{"time":"2026-11-19T22:41:03.512Z","level":"ERROR","msg":"cannot parse csv data:","error":"record on line 152: wrong number of fields"}`
	slogModule = `{"time":"2026-11-19T22:41:03Z","level":"ERROR","msg":"cannot parse:","error":"bad row","module":"csv"}`
	zapEtcd    = `{"level":"warn","ts":"2026-11-19T22:43:11.123456Z","caller":"server/util.go:170","msg":"apply request took too long","took":"123ms","expected-duration":"100ms"}`
	zapEpoch   = `{"level":"info","ts":1795129202.193,"msg":"synced","service":"sync-agent"}`
	zapCilium  = `{"time":"2026-11-19T22:44:00Z","level":"error","msg":"Unable to update cache map entry","subsys":"agent.datapath","ipAddr":"10.0.0.7"}`
	zapModule  = `{"level":"info","ts":"2026-11-19T22:44:00Z","msg":"Starting","module":"envoy"}`
)

func TestJSONLogFixtures_AreValidJSONObjectsWithALevelAndAMessage(t *testing.T) {
	for name, line := range map[string]string{
		"slogError": slogError, "slogModule": slogModule, "zapEtcd": zapEtcd, "zapEpoch": zapEpoch, "zapCilium": zapCilium, "zapModule": zapModule,
	} {
		o, err := parseJSONObject(line)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if o.str("level") == "" || o.str("msg") == "" {
			t.Errorf("%s has no level or no msg", name)
		}
	}
}
