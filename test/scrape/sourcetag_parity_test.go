// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"context"
	"reflect"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/sourcetagprocessor"
)

const (
	ottlAWS      = "testdata/ottl-aws-deployed.yaml"
	ottlAWSS3    = "testdata/ottl-aws-s3access.yaml"
	ottlGCP      = "testdata/ottl-gcp-deployed.yaml"
	ottlAzure    = "testdata/ottl-azure.yaml"
	auditID      = "cloudaudit.googleapis.com%2Factivity"
	ekAuditGroup = "/aws/eks/prod/cluster"
)

type maker func(next consumer.Logs) processor.Logs

func ottlMaker(t *testing.T, fixture string) maker {
	t.Helper()
	var block map[string]any
	if err := yaml.Unmarshal(readFile(t, fixture), &block); err != nil {
		t.Fatalf("parse %s: %v", fixture, err)
	}
	return func(next consumer.Logs) processor.Logs {
		factory := transformprocessor.NewFactory()
		cfg := factory.CreateDefaultConfig()
		if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
			t.Fatalf("unmarshal %s: %v", fixture, err)
		}
		proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, next)
		if err != nil {
			t.Fatalf("create transform from %s: %v", fixture, err)
		}
		return proc
	}
}

func tagMaker(t *testing.T, provider string) maker {
	t.Helper()
	return func(next consumer.Logs) processor.Logs {
		factory := sourcetagprocessor.NewFactory()
		proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()),
			&sourcetagprocessor.Config{Provider: provider}, next)
		if err != nil {
			t.Fatalf("create sourcetag %s: %v", provider, err)
		}
		return proc
	}
}

type snapshot struct {
	Resource, Scope, Attrs map[string]any
	Body                   any
	Severity               plog.SeverityNumber
	Text                   string
}

func snapshotsOf(ld plog.Logs) []snapshot {
	var out []snapshot
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				out = append(out, snapshot{
					Resource: rl.Resource().Attributes().AsRaw(), Scope: sl.Scope().Attributes().AsRaw(),
					Attrs: lr.Attributes().AsRaw(), Body: lr.Body().AsRaw(),
					Severity: lr.SeverityNumber(), Text: lr.SeverityText(),
				})
			}
		}
	}
	return out
}

// runChain feeds fresh input through the processors in order and snapshots what the last one forwards.
func runChain(t *testing.T, in func(*testing.T) plog.Logs, makers ...maker) []snapshot {
	t.Helper()
	sink := new(consumertest.LogsSink)
	var next consumer.Logs = sink
	var head processor.Logs
	for i := len(makers) - 1; i >= 0; i-- {
		head = makers[i](next)
		next = head
	}
	if err := head.ConsumeLogs(context.Background(), in(t)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllLogs()))
	}
	return snapshotsOf(sink.AllLogs()[0])
}

// built makes one record from raw attribute maps; nil maps stay empty.
func built(res, scope, attrs map[string]any, body string, sev plog.SeverityNumber, text string) func(*testing.T) plog.Logs {
	return func(t *testing.T) plog.Logs {
		t.Helper()
		ld := plog.NewLogs()
		rl := ld.ResourceLogs().AppendEmpty()
		sl := rl.ScopeLogs().AppendEmpty()
		lr := sl.LogRecords().AppendEmpty()
		for _, pair := range []struct {
			dst interface{ FromRaw(map[string]any) error }
			raw map[string]any
		}{{rl.Resource().Attributes(), res}, {sl.Scope().Attributes(), scope}, {lr.Attributes(), attrs}} {
			if err := pair.dst.FromRaw(pair.raw); err != nil {
				t.Fatal(err)
			}
		}
		if body != "" {
			lr.Body().SetStr(body)
		}
		lr.SetSeverityNumber(sev)
		lr.SetSeverityText(text)
		return ld
	}
}

func decoded(dec func(*testing.T) logsDecoder, data func(*testing.T) []byte) func(*testing.T) plog.Logs {
	return func(t *testing.T) plog.Logs {
		t.Helper()
		ld, err := dec(t).UnmarshalLogs(data(t))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return ld
	}
}

func file(path string) func(*testing.T) []byte {
	return func(t *testing.T) []byte { return readFile(t, path) }
}

func compactFile(path string) func(*testing.T) []byte {
	return func(t *testing.T) []byte { return compact(t, readFile(t, path)) }
}

func cloudRunEntry(i int) func(*testing.T) []byte {
	return func(t *testing.T) []byte { return cloudRunEntries(t)[i] }
}

type parityCase struct {
	name     string
	provider string
	fixture  string
	input    func(*testing.T) plog.Logs
}

var (
	none   = map[string]any(nil)
	ctScp  = map[string]any{"encoding.format": "aws.cloudtrail"}
	ekAudt = map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "kube-apiserver-audit-1"}
)

func awsCases() []parityCase {
	unspec := plog.SeverityNumberUnspecified
	aws := func(name string, in func(*testing.T) plog.Logs) parityCase {
		return parityCase{name, "aws", ottlAWS, in}
	}
	return []parityCase{
		aws("cloudtrail decoder with an error entry", decoded(cloudTrailDecoder, file("testdata/cloudtrail-multi.json"))),
		{"s3 access decoder", "aws", ottlAWSS3, decoded(func(t *testing.T) logsDecoder {
			return newDecoder(t, awslogsencodingextension.NewFactory(), map[string]any{"format": "s3access"})
		}, file("testdata/s3access.log"))},
		aws("alb decoder", decoded(newALBDecoder, file("testdata/alb-access.log"))),
		aws("eks audit stream", built(ekAudt, none, none, "m", unspec, "")),
		aws("eks audit stream with region", built(map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "kube-apiserver-audit-1", "aws.region": "eu-central-1"}, none, none, "", unspec, "")),
		aws("eks other stream", built(map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "authenticator-1"}, none, none, "", unspec, "")),
		aws("eks without stream", built(map[string]any{"cloudwatch.log.group.name": ekAuditGroup}, none, none, "", unspec, "")),
		aws("eks with a preset category", built(ekAudt, none, map[string]any{"event.category": "security"}, "", unspec, "")),
		aws("lambda group", built(map[string]any{"cloudwatch.log.group.name": "/aws/lambda/api", "aws.region": "us-east-1"}, none, none, "START", unspec, "")),
		aws("stream without a group", built(map[string]any{"cloudwatch.log.stream": "kube-apiserver-audit-1"}, none, none, "", unspec, "")),
		aws("group not under eks", built(map[string]any{"cloudwatch.log.group.name": "/eks/other"}, none, none, "", unspec, "")),
		aws("preset auth0 source", built(map[string]any{"cloudwatch.log.group.name": ekAuditGroup}, none, map[string]any{"log.source": "auth0"}, "", plog.SeverityNumberError, "ERROR")),
		aws("preset source under a cloudtrail scope", built(none, ctScp, map[string]any{"log.source": "auth0", "rpc.method": "a.b/C"}, "", unspec, "")),
		aws("preset severity with text", built(none, none, none, "", plog.SeverityNumberError, "boom")),
		aws("preset info with other text", built(none, none, none, "", plog.SeverityNumberInfo, "notice")),
		aws("cloudtrail error over preset error severity", built(none, ctScp, map[string]any{"rpc.method": "a/b/c", "aws.error.code": "X"}, "", plog.SeverityNumberError, "ERROR")),
		aws("cloudtrail without rpc method", built(none, ctScp, none, "x/y", unspec, "")),
		aws("with region", built(map[string]any{"aws.region": "us-east-1"}, none, none, "", unspec, "")),
		aws("with region and a provider", built(map[string]any{"aws.region": "us-east-1", "cloud.provider": "x", "cloud.region": "y"}, none, none, "", unspec, "")),
		aws("without region", built(none, none, none, "", unspec, "")),
	}
}

func gcpCases() []parityCase {
	unspec := plog.SeverityNumberUnspecified
	gcp := func(name string, in func(*testing.T) plog.Logs) parityCase {
		return parityCase{name, "gcp", ottlGCP, in}
	}
	typed := func(typ string) func(*testing.T) plog.Logs {
		return built(map[string]any{"gcp.resource_type": typ}, none, none, "", unspec, "")
	}
	cases := []parityCase{
		gcp("cloud run decoder probe", decoded(gcpDecoder, cloudRunEntry(0))),
		gcp("cloud run decoder text", decoded(gcpDecoder, cloudRunEntry(2))),
		gcp("cloud run decoder json", decoded(gcpDecoder, cloudRunEntry(3))),
		gcp("audit decoder", decoded(gcpDecoder, compactFile("testdata/gcp-audit-entry.json"))),
		gcp("audit resource id", built(map[string]any{"cloud.resource_id": auditID}, none, none, "", unspec, "")),
		gcp("audit over a preset source", built(map[string]any{"cloud.resource_id": auditID}, none, map[string]any{"log.source": "other"}, "", unspec, "")),
		gcp("cloud run type", typed("cloud_run_revision")),
		gcp("cloud run over a preset source", built(map[string]any{"gcp.resource_type": "cloud_run_revision"}, none, map[string]any{"log.source": "other"}, "", unspec, "")),
		gcp("lb over a preset source", built(map[string]any{"gcp.resource_type": "http_load_balancer"}, none, map[string]any{"log.source": "other"}, "", unspec, "")),
		gcp("unmatched type", typed("gce_instance")),
		gcp("near-miss lb type", typed("http_load_balancer_v2")),
		gcp("labels and project", built(map[string]any{"gcp.label.service_name": "api", "gcp.label.location": "us-central1", "gcp.label.project_id": "p1", "gcp.project": "p2"}, none, none, "", unspec, "")),
		gcp("project only", built(map[string]any{"gcp.project": "p2", "cloud.provider": "other"}, none, none, "", unspec, "")),
		gcp("no labels", built(none, none, none, "", unspec, "")),
	}
	for _, typ := range []string{"http_load_balancer", "l7_lb_rule", "https_lb_rule"} {
		cases = append(cases, gcp("lb type "+typ, typed(typ)))
	}
	return cases
}

func azureCases() []parityCase {
	return []parityCase{
		{"azure plain", "azure", ottlAzure, built(none, none, none, "m", plog.SeverityNumberUnspecified, "")},
		{"azure preset", "azure", ottlAzure, built(map[string]any{"cloud.provider": "x"}, none, map[string]any{"log.source": "x", "event.category": "y"}, "", plog.SeverityNumberError, "ERROR")},
	}
}

func allCases() []parityCase {
	return append(append(awsCases(), gcpCases()...), azureCases()...)
}

func TestSourceTagMatchesTheOTTLItReplaces(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.name, func(t *testing.T) {
			want := runChain(t, c.input, ottlMaker(t, c.fixture))
			got := runChain(t, c.input, tagMaker(t, c.provider))

			if len(want) == 0 {
				t.Fatal("the input produced no records")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("sourcetag differs from the OTTL block\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestOTTLThenSourceTagEqualsSourceTagAlone(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.name, func(t *testing.T) {
			want := runChain(t, c.input, tagMaker(t, c.provider))
			got := runChain(t, c.input, ottlMaker(t, c.fixture), tagMaker(t, c.provider))

			if !reflect.DeepEqual(got, want) {
				t.Errorf("OTTL then sourcetag differs from sourcetag\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestSourceTagThenOTTLEqualsOTTLAlone(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.name, func(t *testing.T) {
			want := runChain(t, c.input, ottlMaker(t, c.fixture))
			got := runChain(t, c.input, tagMaker(t, c.provider), ottlMaker(t, c.fixture))

			if !reflect.DeepEqual(got, want) {
				t.Errorf("sourcetag then OTTL differs from OTTL\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}
