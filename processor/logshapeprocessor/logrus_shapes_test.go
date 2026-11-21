// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore etcdserver subsys vstan

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestLogrus_SlogLine_GivesTheTextTheErrorAndTheTime(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"time":"2026-11-20T04:40:02.123456Z","level":"ERROR","msg":"cannot parse csv data:","error":"record on line 152: wrong number of fields"}`})

	if lr.Body().Str() != "cannot parse csv data: record on line 152: wrong number of fields" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	if attrStr(t, lr.Attributes(), "exception.message") != "record on line 152: wrong number of fields" {
		t.Errorf("attributes = %v", lr.Attributes().AsRaw())
	}
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
	if want := time.Date(2026, 11, 20, 4, 40, 2, 123456000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestLogrus_CiliumLine_PutsTheModuleInFrontOfTheMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"time":"2026-11-19T23:00:12Z","level":"error","msg":"endpoint regeneration failed","subsys":"agent.datapath","ipAddr":"10.0.0.5","nodeID":3}`})

	if lr.Body().Str() != "[agent.datapath] endpoint regeneration failed" {
		t.Errorf("body = %q", lr.Body().Str())
	}
	a := lr.Attributes()
	if attrStr(t, a, "vstan.logrus.ipAddr") != "10.0.0.5" || !hasInt(a, "vstan.logrus.nodeID", 3) {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "vstan.logrus.subsys", "vstan.logrus.module")
	if lr.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %d", lr.SeverityNumber())
	}
}

func TestLogrus_ModuleKeyIsReadLikeSubsys(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"time":"2026-11-19T23:00:12Z","level":"info","msg":"started","module":"agent.controlplane"}`})

	if lr.Body().Str() != "[agent.controlplane] started" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestLogrus_EtcdZapLine_GivesTheCallerTheTimeAndTheFields(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"warn","ts":"2026-01-02T05:00:25.573798Z","caller":"etcdserver/util.go:170","msg":"apply request took too long","took":"153ms","expected-duration":"100ms"}`})

	if lr.Body().Str() != "apply request took too long" || lr.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("body %q severity %d", lr.Body().Str(), lr.SeverityNumber())
	}
	a := lr.Attributes()
	if attrStr(t, a, "code.file.path") != "etcdserver/util.go" || attrStr(t, a, "vstan.logrus.took") != "153ms" || attrStr(t, a, "vstan.logrus.expected-duration") != "100ms" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "code.line.number", 170)
	if want := time.Date(2026, 1, 2, 5, 0, 25, 573798000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestLogrus_ZapEpochTime_IsReadAndServiceAndHostnameAreDropped(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"info","ts":1795129202.123,"caller":"main.go:10","msg":"started","service":"host-sync-bot","hostname":"h1","version":"1.2"}`})

	a := lr.Attributes()
	absent(t, a, "vstan.logrus.service", "vstan.logrus.hostname")
	if attrStr(t, a, "vstan.logrus.version") != "1.2" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	if lr.Timestamp().AsTime().Year() != 2026 {
		t.Errorf("time = %v", lr.Timestamp().AsTime())
	}
}

func TestLogrus_LevelCase_DoesNotMatter(t *testing.T) {
	h := newHarness(t, defaultConfig())
	for _, level := range []string{"INFO", "Info", "info"} {
		lr, _ := h.run(input{body: `{"level":"` + level + `","msg":"x","time":"2026-11-19T23:00:12Z"}`})
		if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
			t.Errorf("%s: %d %q", level, lr.SeverityNumber(), lr.SeverityText())
		}
	}
}

func TestLogrus_ErrorObject_GivesTypeAndMessage(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"error","time":"2026-11-19T23:00:12Z","msg":"call failed","error":{"type":"TimeoutError","message":"deadline exceeded"}}`})

	a := lr.Attributes()
	if attrStr(t, a, "exception.type") != "TimeoutError" || attrStr(t, a, "exception.message") != "deadline exceeded" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
	if lr.Body().Str() != "call failed: deadline exceeded" {
		t.Errorf("body = %q", lr.Body().Str())
	}
}

func TestLogrus_LoggerName_BecomesTheScopeName(t *testing.T) {
	h := newHarness(t, defaultConfig())

	out := h.runBatch(input{body: `{"level":"info","ts":"2026-11-19T23:00:12Z","logger":"controller.runtime","msg":"x"}`, scope: "base"}.logs())

	if got := out.ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name(); got != "controller.runtime" {
		t.Errorf("scope name = %q", got)
	}
	absent(t, out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes(), "vstan.logrus.logger")
}

func TestLogrus_NestedObjects_AreIgnored(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"info","ts":"2026-11-19T23:00:12Z","msg":"x","resource":{"a":1},"list":[1,2]}`})

	absent(t, lr.Attributes(), "vstan.logrus.resource", "vstan.logrus.list")
}

func hasInt(m pcommon.Map, key string, want int64) bool {
	v, ok := m.Get(key)
	return ok && v.Type() == pcommon.ValueTypeInt && v.Int() == want
}

func TestLogrus_MessageEndingInAColon_GetsTheErrorAfterOneColon(t *testing.T) {
	h := newHarness(t, defaultConfig())

	plain, _ := h.run(input{body: `{"time":"2026-11-19T22:41:03.512Z","level":"ERROR","msg":"cannot parse csv data:","error":"record on line 152: wrong number of fields"}`})
	module, _ := h.run(input{body: `{"time":"2026-11-19T22:41:03Z","level":"ERROR","msg":"cannot parse:","error":"bad row","module":"csv"}`})

	if plain.Body().Str() != "cannot parse csv data: record on line 152: wrong number of fields" {
		t.Errorf("body = %q", plain.Body().Str())
	}
	if module.Body().Str() != "[csv] cannot parse: bad row" {
		t.Errorf("body = %q", module.Body().Str())
	}
}

func TestLogrus_NumberAndBooleanExtras_KeepTheirTypes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"info","ts":"2026-11-19T23:00:12Z","msg":"x","count":3,"ratio":0.5,"ok":true,"name":"n"}`})

	a := lr.Attributes()
	attrInt(t, a, "vstan.logrus.count", 3)
	if v, _ := a.Get("vstan.logrus.ratio"); v.Type() != pcommon.ValueTypeDouble || v.Double() != 0.5 {
		t.Errorf("ratio = %v (%s)", v.AsRaw(), v.Type())
	}
	if v, _ := a.Get("vstan.logrus.ok"); v.Type() != pcommon.ValueTypeBool || !v.Bool() {
		t.Errorf("ok = %v (%s)", v.AsRaw(), v.Type())
	}
	if attrStr(t, a, "vstan.logrus.name") != "n" {
		t.Errorf("attributes = %v", a.AsRaw())
	}
}

func TestLogrus_ZapConsoleContext_KeepsNumberTypes(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "2026-01-02T14:18:43.362Z\twarn\tinternal/retry_sender.go:133\tExporting failed.\t{\"attempt\":2}"})

	attrInt(t, lr.Attributes(), "vstan.logrus.attempt", 2)
}

func TestLogrus_BadTime_IsCountedAndTheRecordIsKept(t *testing.T) {
	for name, line := range map[string]string{
		"text that is no time":        `{"level":"info","ts":"not a time","msg":"x"}`,
		"epoch out of range":          `{"level":"info","ts":12,"msg":"x"}`,
		"logrus text with a bad time": `time="yesterday" level=info msg="x" key=v`,
		"zap console with a bad time": "2026-13-45T99:99:99Z\tinfo\tmain.go:1\tx\t{}",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, defaultConfig())

			lr, _ := h.run(input{body: line, ts: recordTime})

			if lr.Body().Str() != line || lr.Attributes().Len() != 0 || !lr.Timestamp().AsTime().Equal(recordTime) {
				t.Errorf("the record changed: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
			}
			if got := h.counter(parseFailedMetric, "format"); got["logrus"] != 1 {
				t.Errorf("parse_failed = %v", got)
			}
		})
	}
}

func TestLogrus_BadTimeWithParseTimestampOff_IsNotAFailure(t *testing.T) {
	cfg := defaultConfig()
	cfg.Logrus.ParseTimestamp = false
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: `{"level":"info","ts":12,"msg":"x"}`, ts: recordTime})

	if lr.Body().Str() != "x" || !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Errorf("body %q time %v", lr.Body().Str(), lr.Timestamp().AsTime())
	}
}

func TestLogrus_NoTimeAtAll_IsNotAFailure(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `{"level":"info","msg":"no time here"}`, ts: recordTime})

	if lr.Body().Str() != "no time here" || !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Errorf("body %q time %v", lr.Body().Str(), lr.Timestamp().AsTime())
	}
}
