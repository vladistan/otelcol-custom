// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension"
	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/googlecloudlogentryencodingextension"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/healthprobesampler"
)

const referenceConfig = "../configs/scrape.yaml"

type logsDecoder interface {
	UnmarshalLogs([]byte) (plog.Logs, error)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func loadReference(t *testing.T) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := yaml.Unmarshal(readFile(t, referenceConfig), &cfg); err != nil {
		t.Fatalf("parse %s: %v", referenceConfig, err)
	}
	return cfg
}

func section(t *testing.T, cfg map[string]any, kind, name string) map[string]any {
	t.Helper()
	block, ok := cfg[kind].(map[string]any)[name].(map[string]any)
	if !ok {
		t.Fatalf("%s.%s missing from %s", kind, name, referenceConfig)
	}
	return block
}

// newTransform builds a transformprocessor from a block of the reference config.
func newTransform(t *testing.T, name string, sink *consumertest.LogsSink) processor.Logs {
	t.Helper()
	block := section(t, loadReference(t), "processors", name)
	factory := transformprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return proc
}

func newDecoder(t *testing.T, factory extension.Factory, block map[string]any) logsDecoder {
	t.Helper()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", factory.Type(), err)
	}
	ext, err := factory.Create(context.Background(), extensiontest.NewNopSettings(factory.Type()), cfg)
	if err != nil {
		t.Fatalf("create %s: %v", factory.Type(), err)
	}
	if err := ext.Start(context.Background(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start %s: %v", factory.Type(), err)
	}
	dec, ok := ext.(logsDecoder)
	if !ok {
		t.Fatalf("%s does not decode logs", factory.Type())
	}
	return dec
}

func cloudTrailDecoder(t *testing.T) logsDecoder {
	return newDecoder(t, awslogsencodingextension.NewFactory(), map[string]any{"format": "cloudtrail"})
}

func gcpDecoder(t *testing.T) logsDecoder {
	return newDecoder(t, googlecloudlogentryencodingextension.NewFactory(), nil)
}

// through decodes data and runs the result through proc, returning what proc forwards.
func through(t *testing.T, dec logsDecoder, proc processor.Logs, sink *consumertest.LogsSink, data []byte) plog.Logs {
	t.Helper()
	decoded, err := dec.UnmarshalLogs(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	sink.Reset()
	if err := proc.ConsumeLogs(context.Background(), decoded); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllLogs()))
	}
	return sink.AllLogs()[0]
}

type record struct {
	resource pcommon.Map
	log      plog.LogRecord
}

func recordsOf(ld plog.Logs) []record {
	var out []record
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				out = append(out, record{resource: rl.Resource().Attributes(), log: sl.LogRecords().At(k)})
			}
		}
	}
	return out
}

func wantStr(t *testing.T, m pcommon.Map, key, want string) {
	t.Helper()
	got, ok := m.Get(key)
	if !ok || got.Str() != want {
		t.Fatalf("%s = %q (present=%v), want %q", key, got.Str(), ok, want)
	}
}

func wantAbsent(t *testing.T, m pcommon.Map, key string) {
	t.Helper()
	if v, ok := m.Get(key); ok {
		t.Fatalf("%s = %v, want it absent", key, v.AsRaw())
	}
}

func TestCloudTrailGivesOneRecordPerEntry(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/aws", sink)
	out := through(t, cloudTrailDecoder(t), proc, sink, readFile(t, "testdata/cloudtrail-multi.json"))

	recs := recordsOf(out)
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3 (one per Records[] entry)", len(recs))
	}
	wantBody := []string{"s3.amazonaws.com GetObject", "iam.amazonaws.com CreateUser", "ec2.amazonaws.com DescribeInstances"}
	wantSev := []plog.SeverityNumber{plog.SeverityNumberInfo, plog.SeverityNumberWarn, plog.SeverityNumberInfo}
	for i, r := range recs {
		wantStr(t, r.log.Attributes(), "log.source", "aws.cloudtrail")
		wantStr(t, r.log.Attributes(), "event.category", "audit")
		wantStr(t, r.resource, "cloud.provider", "aws")
		wantStr(t, r.resource, "cloud.account.id", "111122223333")
		if got := r.log.Body().Str(); got != wantBody[i] {
			t.Fatalf("record %d body = %q, want %q", i, got, wantBody[i])
		}
		if r.log.SeverityNumber() != wantSev[i] {
			t.Fatalf("record %d severity = %v, want %v", i, r.log.SeverityNumber(), wantSev[i])
		}
	}
	if got := recs[1].log.SeverityText(); got != "WARN" {
		t.Fatalf("errorCode record severity text = %q, want WARN", got)
	}
}

func TestS3AccessLogSource(t *testing.T) {
	dec := newDecoder(t, awslogsencodingextension.NewFactory(), map[string]any{"format": "s3access"})
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/aws", sink)
	recs := recordsOf(through(t, dec, proc, sink, readFile(t, "testdata/s3access.log")))
	if len(recs) == 0 {
		t.Fatal("no records from the S3 access log line")
	}
	for _, r := range recs {
		wantStr(t, r.log.Attributes(), "log.source", "aws.s3access")
		wantStr(t, r.log.Attributes(), "event.category", "access")
		if r.log.SeverityNumber() != plog.SeverityNumberInfo {
			t.Fatalf("severity = %v, want INFO", r.log.SeverityNumber())
		}
	}
}

func TestCloudWatchLogsSource(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/aws", sink)

	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("aws.region", "us-east-1")
	rl.Resource().Attributes().PutStr("cloudwatch.log.group.name", "/aws/lambda/api")
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("START RequestId: 1")
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	recs := recordsOf(sink.AllLogs()[0])
	wantStr(t, recs[0].log.Attributes(), "log.source", "aws.cloudwatch")
	wantStr(t, recs[0].resource, "cloud.provider", "aws")
	wantStr(t, recs[0].resource, "cloud.region", "us-east-1")
}

func TestAuth0RecordKeepsItsOwnSource(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/aws", sink)

	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Attributes().PutStr("log.source", "auth0")
	lr.SetSeverityNumber(plog.SeverityNumberError)
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(sink.AllLogs()[0])[0].log
	wantStr(t, got.Attributes(), "log.source", "auth0")
	if got.SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("auth0 severity = %v, want ERROR kept", got.SeverityNumber())
	}
}

// compact puts a fixture on one line, as Pub/Sub delivers a log entry; the
// googlecloudlogentry decoder rejects pretty-printed JSON.
func compact(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func cloudRunEntries(t *testing.T) [][]byte {
	t.Helper()
	var entries []json.RawMessage
	if err := json.Unmarshal(readFile(t, "testdata/cloudrun-entries.json"), &entries); err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = compact(t, e)
	}
	return out
}

func TestCloudRunMapping(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/gcp", sink)
	dec := gcpDecoder(t)
	entries := cloudRunEntries(t)

	probe := recordsOf(through(t, dec, proc, sink, entries[0]))[0]
	wantStr(t, probe.log.Attributes(), "log.source", "gcp.cloudrun")
	wantStr(t, probe.log.Attributes(), "event.category", "application")
	wantStr(t, probe.log.Attributes(), "url.path", "/healthz")
	wantStr(t, probe.log.Attributes(), "user_agent.original", "kube-probe/1.29")
	if v, ok := probe.log.Attributes().Get("http.response.status_code"); !ok || v.Int() != 200 {
		t.Fatalf("http.response.status_code = %v (present=%v), want 200", v.AsRaw(), ok)
	}
	wantStr(t, probe.resource, "service.name", "api")
	wantStr(t, probe.resource, "cloud.region", "us-central1")
	wantStr(t, probe.resource, "cloud.account.id", "proj-example")
	wantStr(t, probe.resource, "cloud.provider", "gcp")

	text := recordsOf(through(t, dec, proc, sink, entries[2]))[0]
	wantStr(t, text.log.Attributes(), "log.source", "gcp.cloudrun")
	for _, key := range []string{"http.response.status_code", "url.path", "user_agent.original"} {
		wantAbsent(t, text.log.Attributes(), key)
	}
	if got := text.log.Body().Str(); got != "listening on port 8080" {
		t.Fatalf("textPayload body = %q", got)
	}

	jsonRec := recordsOf(through(t, dec, proc, sink, entries[3]))[0]
	wantStr(t, jsonRec.log.Attributes(), "log.source", "gcp.cloudrun")
	if jsonRec.log.SeverityNumber() != plog.SeverityNumberError {
		t.Fatalf("jsonPayload severity = %v, want ERROR kept", jsonRec.log.SeverityNumber())
	}
	if jsonRec.log.Body().Type() != pcommon.ValueTypeMap {
		t.Fatalf("jsonPayload body type = %v, want map", jsonRec.log.Body().Type())
	}
}

func TestAuditEntrySource(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/gcp", sink)
	rec := recordsOf(through(t, gcpDecoder(t), proc, sink, compact(t, readFile(t, "testdata/gcp-audit-entry.json"))))[0]
	wantStr(t, rec.log.Attributes(), "log.source", "gcp.audit")
	wantStr(t, rec.log.Attributes(), "event.category", "audit")
	wantStr(t, rec.resource, "cloud.account.id", "proj-example")
	wantAbsent(t, rec.resource, "service.name")
}

// sampler runs mapped records through healthprobesampler with the given keep ratio.
func sampler(t *testing.T, keepRatio float64, sink *consumertest.LogsSink) processor.Logs {
	t.Helper()
	factory := healthprobesampler.NewFactory()
	cfg := factory.CreateDefaultConfig().(*healthprobesampler.Config)
	cfg.KeepRatio = keepRatio
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create healthprobesampler: %v", err)
	}
	return proc
}

// mapped returns the Cloud Run entries after the logs/gcp transform.
func mapped(t *testing.T) []plog.Logs {
	t.Helper()
	sink := new(consumertest.LogsSink)
	proc := newTransform(t, "transform/gcp", sink)
	dec := gcpDecoder(t)
	var out []plog.Logs
	for _, entry := range cloudRunEntries(t) {
		ld := plog.NewLogs()
		through(t, dec, proc, sink, entry).CopyTo(ld)
		out = append(out, ld)
	}
	return out
}

func TestProbeRecordIsClassedAsProbe(t *testing.T) {
	entries := mapped(t)

	dropAll := new(consumertest.LogsSink)
	sample := sampler(t, 0, dropAll)
	if err := sample.ConsumeLogs(context.Background(), entries[0]); err != nil {
		t.Fatal(err)
	}
	if got := dropAll.LogRecordCount(); got != 0 {
		t.Fatalf("a 200 on /healthz from kube-probe survived keep_ratio 0 (%d records)", got)
	}

	keepAll := new(consumertest.LogsSink)
	sample = sampler(t, 1, keepAll)
	probe := plog.NewLogs()
	mapped(t)[0].CopyTo(probe)
	if err := sample.ConsumeLogs(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	kept := recordsOf(keepAll.AllLogs()[0])[0]
	wantStr(t, kept.log.Attributes(), "log.type", "health_probe")
}

// flatten lists the dotted key paths under v; list items of maps add "[]".
func flatten(prefix string, v any, out *[]string) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			flatten(join(k), child, out)
		}
	case []any:
		for _, item := range val {
			if _, isMap := item.(map[string]any); isMap {
				flatten(prefix+"[]", item, out)
				continue
			}
			*out = append(*out, prefix)
			return
		}
	default:
		*out = append(*out, prefix)
	}
}

func TestReferenceKeysHaveReviewRows(t *testing.T) {
	cfg := loadReference(t)
	doc := string(readFile(t, "../../docs/scrape-collector.md"))
	checked := 0
	defer func() {
		if checked == 0 {
			t.Error("no reference config keys were checked")
		}
	}()
	for _, kind := range []string{"receivers", "extensions"} {
		for name, block := range cfg[kind].(map[string]any) {
			typ := strings.SplitN(name, "/", 2)[0]
			if kind == "extensions" && !strings.HasSuffix(typ, "_encoding") {
				continue
			}
			var keys []string
			flatten("", block, &keys)
			for _, key := range keys {
				checked++
				if row := "| `" + typ + "` | `" + key + "` |"; !strings.Contains(doc, row) {
					t.Errorf("%s.%s: no review table row %q in docs/scrape-collector.md", kind, name, row)
				}
			}
		}
	}
}

func TestOrdinaryRecordsPassTheSamplerUnchanged(t *testing.T) {
	for i, name := range map[int]string{1: "200 on a normal path", 2: "textPayload without httpRequest", 3: "jsonPayload"} {
		sink := new(consumertest.LogsSink)
		sample := sampler(t, 0, sink)
		ld := mapped(t)[i]
		if err := sample.ConsumeLogs(context.Background(), ld); err != nil {
			t.Fatal(err)
		}
		if got := sink.LogRecordCount(); got != 1 {
			t.Fatalf("%s: records after keep_ratio 0 = %d, want 1", name, got)
		}
		wantAbsent(t, recordsOf(sink.AllLogs()[0])[0].log.Attributes(), "log.type")
	}
}
