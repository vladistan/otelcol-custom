// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"maps"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	klogLine    = `I1119 23:05:39.184859       1 client.go:214] "Connect to server" serverID="903ebd76"`
	uvicornLine = `INFO:     198.51.100.145:12610 - "GET /healthz HTTP/1.1" 200 OK`
)

var recordTime = time.Date(2026, 11, 19, 23, 5, 40, 0, time.UTC)

func TestConsume_KlogRecord_ReachesTheKlogBranch(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: klogLine, ts: recordTime})

	if got := lr.Body().Str(); got != "Connect to server serverID=903ebd76" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	a := lr.Attributes()
	attrInt(t, a, "process.pid", 1)
	attrInt(t, a, "code.line.number", 214)
	if attrStr(t, a, "code.file.path") != "client.go" || attrStr(t, a, "log.record.original") != klogLine {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	absent(t, a, "code.filepath", "original_message", "log.level")
	if want := time.Date(2026, 11, 19, 23, 5, 39, 184859000, time.UTC); !lr.Timestamp().AsTime().Equal(want) {
		t.Fatalf("time = %v, want %v", lr.Timestamp().AsTime(), want)
	}
}

func TestConsume_UvicornAccessRecord_ReachesTheUvicornBranch(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: uvicornLine})

	if got := lr.Body().Str(); got != "GET /healthz 200" {
		t.Fatalf("body = %q", got)
	}
	if lr.SeverityNumber() != plog.SeverityNumberInfo || lr.SeverityText() != "INFO" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
	a := lr.Attributes()
	if attrStr(t, a, "http.request.method") != "GET" || attrStr(t, a, "url.path") != "/healthz" || attrStr(t, a, "client.address") != "198.51.100.145" {
		t.Fatalf("attributes = %v", a.AsRaw())
	}
	attrInt(t, a, "http.response.status_code", 200)
	attrInt(t, a, "client.port", 12610)
	if attrStr(t, a, "log.record.original") != uvicornLine {
		t.Fatalf("original = %v", a.AsRaw())
	}
}

func TestConsume_UvicornNotFound_IsWarnByTheStatusRule(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: `INFO:     10.0.0.1:5000 - "GET /nope HTTP/1.1" 404 Not Found`})

	if lr.SeverityNumber() != plog.SeverityNumberWarn || lr.SeverityText() != "WARN" {
		t.Fatalf("severity = %d %q", lr.SeverityNumber(), lr.SeverityText())
	}
}

func TestConsume_UnmatchedRecord_PassesUnchangedAndIsCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())

	lr, _ := h.run(input{body: "hello world", attrs: map[string]any{"log.source": "docker", "k": "v"}})

	if lr.Body().Str() != "hello world" || lr.SeverityNumber() != plog.SeverityNumberUnspecified || lr.SeverityText() != "" {
		t.Fatalf("record changed: body %q severity %d %q", lr.Body().Str(), lr.SeverityNumber(), lr.SeverityText())
	}
	if want := map[string]any{"log.source": "docker", "k": "v"}; !maps.Equal(lr.Attributes().AsRaw(), want) {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{"docker": 1}) {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestConsume_UnmatchedRecordWithoutASource_IsCountedUnderNone(t *testing.T) {
	h := newHarness(t, defaultConfig())

	h.run(input{body: "hello"})

	if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{"none": 1}) {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestConsume_MatchedRecord_IsNotCountedAsUnmatched(t *testing.T) {
	h := newHarness(t, defaultConfig())

	h.run(input{body: klogLine, ts: recordTime})

	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Fatalf("unmatched = %v, want none", got)
	}
}

func TestConsume_DisabledBranch_PassesTheRecordUnchanged(t *testing.T) {
	cfg := defaultConfig()
	cfg.Branches = []string{"uvicorn"}
	h := newHarness(t, cfg)

	lr, _ := h.run(input{body: klogLine, ts: recordTime})

	if lr.Body().Str() != klogLine || lr.Attributes().Len() != 0 {
		t.Fatalf("a disabled branch changed the record: %q %v", lr.Body().Str(), lr.Attributes().AsRaw())
	}
	if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{"none": 1}) {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestConsume_ClaimedRecordWithABadTimestamp_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = `I1339 25:61:00.000000       1 a.go:1] "message" key=value`

	lr, _ := h.run(input{body: bad, ts: recordTime})

	if lr.Body().Str() != bad || lr.Attributes().Len() != 0 || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
		t.Fatalf("partial write: body %q attrs %v severity %d", lr.Body().Str(), lr.Attributes().AsRaw(), lr.SeverityNumber())
	}
	if !lr.Timestamp().AsTime().Equal(recordTime) {
		t.Fatalf("timestamp changed to %v", lr.Timestamp().AsTime())
	}
	if got := h.counter(parseFailedMetric, "format"); !maps.Equal(got, map[string]int64{"klog": 1}) {
		t.Fatalf("parse_failed = %v", got)
	}
	if got := h.counter(unmatchedMetric, "source"); len(got) != 0 {
		t.Fatalf("a claimed record is not unmatched: %v", got)
	}
}

func TestConsume_ManyRecordsInOneBatch_AreEachRouted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for _, b := range []string{klogLine, "plain", uvicornLine, "plain two"} {
		sl.LogRecords().AppendEmpty().Body().SetStr(b)
	}
	if err := h.proc.ConsumeLogs(t.Context(), ld); err != nil {
		t.Fatal(err)
	}
	out := h.sink.AllLogs()[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	var bodies []string
	for i := range out.Len() {
		bodies = append(bodies, out.At(i).Body().Str())
	}
	if want := "Connect to server serverID=903ebd76|plain|GET /healthz 200|plain two"; strings.Join(bodies, "|") != want {
		t.Fatalf("bodies = %q", bodies)
	}
	if got := h.counter(unmatchedMetric, "source"); !maps.Equal(got, map[string]int64{"none": 2}) {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestConsume_StaticPodRow_GetsItsHostFromThePodName(t *testing.T) {
	h := newHarness(t, defaultConfig())

	_, res := h.run(input{body: klogLine, ts: recordTime, resource: map[string]any{"k8s.pod.name": "kube-apiserver-node-1"}})

	if attrStr(t, res.Attributes(), "host.name") != "node-1" {
		t.Fatalf("resource = %v", res.Attributes().AsRaw())
	}
}

func TestConsume_KlogClusterCIDRInTheMessage_IsNotAClient(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const line = `I1119 23:05:39.184859       1 cidrallocator.go:277] "updated ClusterIP allocator for Service CIDR 203.0.113.0/24"`

	lr, _ := h.run(input{body: line, ts: recordTime})

	absent(t, lr.Attributes(), "client.ip", "client.address")
	if lr.Body().Str() != "updated ClusterIP allocator for Service CIDR 203.0.113.0/24" {
		t.Fatalf("body = %q", lr.Body().Str())
	}
	if attrStr(t, lr.Attributes(), "code.file.path") != "cidrallocator.go" {
		t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
	}
}
