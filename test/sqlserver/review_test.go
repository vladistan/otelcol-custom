// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sqlserver_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.yaml.in/yaml/v3"

	"github.com/vladistan/otelcol-custom/test/sqlserver"
)

const (
	errorlogProcessor = "transform/sqlserver_errorlog"
	receiverModule    = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver"
)

func configWith(t *testing.T, text string) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(text), &doc))
	return doc
}

func TestSecrets_TheGuardReadsTheWholeConfigAndEveryConnectionStringForm(t *testing.T) {
	for name, text := range map[string]string{
		"url with userinfo":              "receivers:\n  sqlserver:\n    datasource: \"sqlserver://sa:hunter2@localhost:1433?encrypt=true\"\n",    // pragma: allowlist secret
		"odbc form":                      "receivers:\n  sqlserver:\n    datasource: \"odbc:server=localhost;user id=u;pwd=hunter2;port=1433\"\n", // pragma: allowlist secret
		"odbc braces":                    "receivers:\n  sqlserver:\n    datasource: \"odbc:server=localhost;PWD={hun;ter2}\"\n",
		"password key":                   "receivers:\n  sqlserver:\n    password: hunter2\n",
		"named receiver":                 "receivers:\n  sqlserver/second:\n    password: hunter2\n",
		"query parameter":                "receivers:\n  sqlserver:\n    datasource: \"sqlserver://u@localhost?password=hunter2\"\n", // pragma: allowlist secret
		"string outside of any receiver": "exporters:\n  other:\n    endpoint: \"sqlserver://sa:hunter2@localhost\"\n",               // pragma: allowlist secret
		"password after a ref":           "receivers:\n  sqlserver:\n    datasource: \"server=x;password=${file:/p};pwd=hunter2\"\n", // pragma: allowlist secret
	} {
		t.Run("flags "+name, func(t *testing.T) {
			assert.NotEmpty(t, sqlserver.LiteralSecrets(configWith(t, text)))
		})
	}
	for name, text := range map[string]string{
		"file reference":             "receivers:\n  sqlserver:\n    datasource: \"server=x;password=${file:C:/p/pw};port=1\"\n",
		"env reference":              "receivers:\n  sqlserver:\n    password: ${env:SQL_PASSWORD}\n",
		"url with a reference":       "receivers:\n  sqlserver:\n    datasource: \"sqlserver://u:${file:/p}@localhost\"\n",
		"odbc with a reference":      "receivers:\n  sqlserver:\n    datasource: \"odbc:server=x;pwd=${env:PW}\"\n",
		"no password at all":         "receivers:\n  sqlserver:\n    datasource: \"server=x;port=1\"\n",
		"another receivers password": "receivers:\n  idrac:\n    password: not-this-guards-business\n",
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			assert.Empty(t, sqlserver.LiteralSecrets(configWith(t, text)))
		})
	}
}

func TestSecrets_TheSampleHasNoLiteralSecret(t *testing.T) {
	assert.Empty(t, sqlserver.LiteralSecrets(rawSample(t)))
}

func samplePipelinesWith(t *testing.T, mutate func(pipelines map[string]any, receivers map[string]any)) map[string]any {
	t.Helper()
	doc := rawSample(t)
	mutate(section(t, doc, "service", "pipelines"), section(t, doc, "receivers"))
	return doc
}

func TestPipelines_TheSampleHasNoProblem(t *testing.T) {
	assert.Empty(t, sqlserver.QueryPipelineProblems(rawSample(t)))
}

func TestPipelines_AnyWayToLetQueryEventsReachAnotherLogExporterIsFlagged(t *testing.T) {
	cases := map[string]func(p map[string]any, r map[string]any){
		"a second sqlserver receiver in the error log pipeline": func(p, r map[string]any) {
			r["sqlserver/second"] = map[string]any{"datasource": "server=x"}
			pl := p["logs/sqlserver_errorlog"].(map[string]any)
			pl["receivers"] = append(pl["receivers"].([]any), "sqlserver/second")
		},
		"a second sqlserver receiver in its own pipeline to the shared exporter": func(p, r map[string]any) {
			r["sqlserver/second"] = map[string]any{"datasource": "server=x"}
			p["logs/second"] = map[string]any{"receivers": []any{"sqlserver/second"}, "processors": []any{"memory_limiter"}, "exporters": []any{"otlp_http"}}
		},
		"the query pipeline without the allowlist processor": func(p, _ map[string]any) {
			p["logs/sqlserver_queries"].(map[string]any)["processors"] = []any{"memory_limiter"}
		},
		"another logs pipeline on the query exporter": func(p, _ map[string]any) {
			p["logs/sqlserver_errorlog"].(map[string]any)["exporters"] = []any{"otlp_http", "otlp_http/sqlserver_queries"}
		},
		"another source in the query pipeline": func(p, _ map[string]any) {
			q := p["logs/sqlserver_queries"].(map[string]any)
			q["receivers"] = append(q["receivers"].([]any), "filelog/sqlserver_errorlog")
		},
		"the query pipeline on the shared exporter": func(p, _ map[string]any) {
			p["logs/sqlserver_queries"].(map[string]any)["exporters"] = []any{"otlp_http"}
		},
		"no pipeline for the query events": func(p, _ map[string]any) { delete(p, "logs/sqlserver_queries") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, sqlserver.QueryPipelineProblems(samplePipelinesWith(t, mutate)))
		})
	}
}

func TestPipelines_AMalformedValueIsAProblemNotSkipped(t *testing.T) {
	cases := map[string]func(p map[string]any){
		"a number among the exporters of another pipeline": func(p map[string]any) {
			p["logs/sqlserver_errorlog"].(map[string]any)["exporters"] = []any{"otlp_http", 7}
		},
		"a number among the processors of the query pipeline": func(p map[string]any) {
			q := p["logs/sqlserver_queries"].(map[string]any)
			q["processors"] = append(q["processors"].([]any), 7)
		},
		"a number among the receivers of the query pipeline": func(p map[string]any) {
			q := p["logs/sqlserver_queries"].(map[string]any)
			q["receivers"] = append(q["receivers"].([]any), 7)
		},
		"exporters that are not a list": func(p map[string]any) {
			p["logs/sqlserver_errorlog"].(map[string]any)["exporters"] = "otlp_http"
		},
		"a pipeline body that is not a map": func(p map[string]any) { p["logs/broken"] = "oops" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			doc := samplePipelinesWith(t, func(p, _ map[string]any) { mutate(p) })
			problems := sqlserver.QueryPipelineProblems(doc)
			assert.True(t, slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, "malformed") }), "%v", problems)
		})
	}
	assert.NotEmpty(t, sqlserver.QueryPipelineProblems(map[string]any{"service": "oops"}))
}

func TestPipelines_AMetricsPipelineWithTheReceiverIsFine(t *testing.T) {
	doc := samplePipelinesWith(t, func(p, _ map[string]any) {
		p["metrics/other"] = map[string]any{"receivers": []any{"sqlserver"}, "exporters": []any{"otlp_http"}}
	})
	assert.Empty(t, sqlserver.QueryPipelineProblems(doc))
}

type metadata struct {
	ResourceAttributes map[string]any `yaml:"resource_attributes"`
	Events             map[string]struct {
		Attributes []string `yaml:"attributes"`
	} `yaml:"events"`
}

// receiverMetadata is metadata.yaml of the receiver module in use: the names of the attributes it emits.
func receiverMetadata(t *testing.T) metadata {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", receiverModule).Output()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "metadata.yaml"))
	require.NoError(t, err)
	var m metadata
	require.NoError(t, yaml.Unmarshal(b, &m))
	require.NotEmpty(t, m.Events)
	return m
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

// keptKeys returns the keys of the keep_keys statement of one context of the allowlist processor.
func keptKeys(t *testing.T, doc map[string]any, context string) []string {
	t.Helper()
	proc := section(t, doc, "processors", sqlserver.AllowlistProcessor)
	statements, ok := proc["log_statements"].([]any)
	require.True(t, ok, "log_statements is a list")
	for _, entry := range statements {
		e, ok := entry.(map[string]any)
		require.True(t, ok, "a log_statements entry is a map")
		if e["context"] != context {
			continue
		}
		var keys []string
		list, ok := e["statements"].([]any)
		require.True(t, ok, "statements is a list")
		for _, st := range list {
			s, ok := st.(string)
			require.True(t, ok, "a statement is a string")
			if strings.HasPrefix(s, "keep_keys(") {
				for _, m := range quoted.FindAllStringSubmatch(s, -1) {
					keys = append(keys, m[1])
				}
			}
		}
		return keys
	}
	t.Fatalf("no %s context in %s", context, sqlserver.AllowlistProcessor)
	return nil
}

func newTransform(t *testing.T, doc map[string]any, name string, next *consumertest.LogsSink) processor.Logs {
	t.Helper()
	factory := transformprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	unmarshal(t, cfg, section(t, doc, "processors", name))
	p, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, next)
	require.NoError(t, err)
	require.NoError(t, p.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

// neverLeave are attributes that hold query text, plan text, application text or a login name.
var neverLeave = []string{"db.query.text", "sqlserver.query_plan", "sqlserver.procedure.definition", "sqlserver.context_info", "user.name"}

func TestAllowlist_EveryAttributeTheReceiverEmitsIsKeptOrDroppedOnPurpose(t *testing.T) {
	doc := rawSample(t)
	meta := receiverMetadata(t)
	kept := keptKeys(t, doc, "log")
	allowed := map[string]bool{}
	for _, k := range kept {
		allowed[k] = true
	}
	for _, k := range neverLeave {
		assert.NotContains(t, kept, k, "%s is on the allowlist", k)
	}

	sink := new(consumertest.LogsSink)
	p := newTransform(t, doc, sqlserver.AllowlistProcessor, sink)

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("x.unknown.resource.text", "SENSITIVE-resource")
	for k := range meta.ResourceAttributes {
		rl.Resource().Attributes().PutStr(k, "kept-"+k)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("receiver-scope")
	sl.Scope().Attributes().PutStr("scope.text", "SENSITIVE-scope")
	var sent []string
	for _, event := range []string{"db.server.top_query", "db.server.query_sample"} {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr("SENSITIVE-body")
		lr.Attributes().PutStr("event.name", event)
		for _, k := range meta.Events[event].Attributes {
			lr.Attributes().PutStr(k, "SENSITIVE-"+k)
			sent = append(sent, k)
		}
		lr.Attributes().PutStr("future.upstream.text", "SENSITIVE-future")
	}
	require.NoError(t, p.ConsumeLogs(context.Background(), logs))

	got := sink.AllLogs()
	require.Len(t, got, 1)
	out := got[0].ResourceLogs().At(0)
	resource := out.Resource().Attributes().AsRaw()
	_, leaked := resource["x.unknown.resource.text"]
	assert.False(t, leaked, "an unknown resource attribute is dropped")
	for k := range meta.ResourceAttributes {
		assert.Equal(t, "kept-"+k, resource[k], "the receiver's own resource attribute %s stays", k)
	}
	assert.Equal(t, "receiver-scope", out.ScopeLogs().At(0).Scope().Name())
	assert.Empty(t, out.ScopeLogs().At(0).Scope().Attributes().AsRaw(), "scope attributes are dropped")

	recs := out.ScopeLogs().At(0).LogRecords()
	require.Equal(t, 2, recs.Len())
	for i := 0; i < recs.Len(); i++ {
		lr := recs.At(i)
		assert.Empty(t, lr.Body().AsString(), "the body is empty")
		attrs := lr.Attributes().AsRaw()
		for k, v := range attrs {
			assert.True(t, allowed[k] || k == "event.name", "attribute %s left the collector with value %v", k, v)
			assert.NotContains(t, v, "SENSITIVE-body")
		}
		for _, k := range neverLeave {
			_, has := attrs[k]
			assert.False(t, has, "%s must not leave", k)
		}
		_, has := attrs["future.upstream.text"]
		assert.False(t, has, "an attribute that a later release adds is dropped until someone allows it")
		assert.Greater(t, lr.Attributes().Len(), 1, "the figures of the slow query stay")
	}
	for _, k := range sent {
		if allowed[k] {
			_, has := recs.At(0).Attributes().Get(k)
			_, has2 := recs.At(1).Attributes().Get(k)
			assert.True(t, has || has2, "%s is allowed but never arrived", k)
		}
	}
}

// A typo in the allowlist would silently keep nothing.
func TestAllowlist_EveryAllowedKeyIsAnAttributeTheReceiverEmits(t *testing.T) {
	emitted := map[string]bool{"event.name": true}
	for _, e := range receiverMetadata(t).Events {
		for _, k := range e.Attributes {
			emitted[k] = true
		}
	}
	for _, k := range keptKeys(t, rawSample(t), "log") {
		assert.True(t, emitted[k], "%s is on the allowlist but the receiver does not emit it", k)
	}
}

const (
	entryOne   = "2026-11-17 10:00:00.11 Server      Microsoft SQL Server 2025 (RTM) - 17.0.1000.7"
	entryTwo   = "2026-11-17 10:00:01.22 Logon       Error: 18456, Severity: 14, State: 8."
	entryCont  = "\tLogin failed for user 'example'. Reason: Password did not match."
	entryThree = "2026-11-17 10:00:02.33 Server      Using dynamic lock allocation."
	entryFour  = "2026-11-17 10:05:00.44 Server      Log was started again."
)

func appendUTF16(t *testing.T, path, text string, bom bool) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write(utf16le(text, bom))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestErrorLog_TheSampleReadsTheLiveFileOnly(t *testing.T) {
	sec := section(t, rawSample(t), "receivers", "filelog/sqlserver_errorlog")
	include, ok := sec["include"].([]any)
	require.True(t, ok, "include is a list")
	require.Len(t, include, 1)
	path, ok := include[0].(string)
	require.True(t, ok, "include holds a string")
	assert.True(t, strings.HasSuffix(path, "/ERRORLOG"), "the glob is anchored to the live file: %s", path)
	assert.NotContains(t, path, "*")
}

func TestErrorLog_RotationOnARestartReadsEachEntryOnceWithoutBOMOrCR(t *testing.T) {
	if testing.Short() {
		t.Skip("waits on file polling")
	}
	dir := t.TempDir()
	live := filepath.Join(dir, "ERRORLOG")
	// The file exists before the collector starts: production reads from the end.
	appendUTF16(t, live, entryOne+"\r\n"+entryTwo+"\r\n"+entryCont+"\r\n", true)

	doc := rawSample(t)
	sec := section(t, doc, "receivers", "filelog/sqlserver_errorlog")
	sec["include"] = []any{filepath.ToSlash(live)}
	sec["poll_interval"] = "20ms"
	delete(sec, "storage")

	sink := new(consumertest.LogsSink)
	clean := newTransform(t, doc, errorlogProcessor, sink)
	factory := filelogreceiver.NewFactory()
	cfg := factory.CreateDefaultConfig()
	unmarshal(t, cfg, sec)
	r, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(factory.Type()), cfg, clean)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	time.Sleep(300 * time.Millisecond)
	assert.Zero(t, sink.LogRecordCount(), "the history of the file is not read at start")

	appendUTF16(t, live, entryThree+"\r\n", false)
	require.Eventually(t, func() bool { return sink.LogRecordCount() >= 1 }, 5*time.Second, 20*time.Millisecond)

	// SQL Server starts a new log: the old file becomes ERRORLOG.1 and a new file begins with a BOM.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.Rename(live, live+".1"))
	appendUTF16(t, live, entryFour+"\r\n"+entryTwo+"\r\n"+entryCont+"\r\n", true)
	require.Eventually(t, func() bool { return sink.LogRecordCount() >= 3 }, 5*time.Second, 20*time.Millisecond)
	time.Sleep(300 * time.Millisecond)

	var bodies []string
	for _, l := range sink.AllLogs() {
		recs := l.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
		for i := 0; i < recs.Len(); i++ {
			bodies = append(bodies, recs.At(i).Body().AsString())
		}
	}
	// No trim: the production body is checked as it leaves the pipeline.
	assert.Equal(t, []string{entryThree, entryFour, entryTwo + "\n" + entryCont}, bodies, "each entry once, no byte order mark, no carriage return")
	for _, b := range bodies {
		assert.NotContains(t, b, "\r")
		assert.False(t, strings.HasPrefix(b, "\uFEFF"))
	}
}
