// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"context"
	"maps"
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

type stageMaker func(next consumer.Logs) processor.Logs

func ottlMaker(t *testing.T, fixture string) stageMaker {
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

func tagMaker(t *testing.T, provider string) stageMaker {
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
func runChain(t *testing.T, in func(*testing.T) plog.Logs, stages ...stageMaker) []snapshot {
	t.Helper()
	sink := new(consumertest.LogsSink)
	var next consumer.Logs = sink
	var head processor.Logs
	for i := len(stages) - 1; i >= 0; i-- {
		head = stages[i](next)
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

// oneRecord makes one record from raw attribute maps; nil maps stay empty.
func oneRecord(res, scope, attrs map[string]any, body string, sev plog.SeverityNumber, text string) func(*testing.T) plog.Logs {
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

func fixtureFile(path string) func(*testing.T) []byte {
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
	noAttrs          = map[string]any(nil)
	cloudTrailScope  = map[string]any{"encoding.format": "aws.cloudtrail"}
	eksAuditResource = map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "kube-apiserver-audit-1"}
)

func awsCases() []parityCase {
	unspecified := plog.SeverityNumberUnspecified
	aws := func(name string, in func(*testing.T) plog.Logs) parityCase {
		return parityCase{name, "aws", ottlAWS, in}
	}
	return []parityCase{
		aws("cloudtrail decoder with an error entry", decoded(cloudTrailDecoder, fixtureFile("testdata/cloudtrail-multi.json"))),
		{"s3 access decoder", "aws", ottlAWSS3, decoded(func(t *testing.T) logsDecoder {
			return newDecoder(t, awslogsencodingextension.NewFactory(), map[string]any{"format": "s3access"})
		}, fixtureFile("testdata/s3access.log"))},
		aws("alb decoder", decoded(newALBDecoder, fixtureFile("testdata/alb-access.log"))),
		aws("eks audit stream", oneRecord(eksAuditResource, noAttrs, noAttrs, "m", unspecified, "")),
		aws("eks audit stream with region", oneRecord(map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "kube-apiserver-audit-1", "aws.region": "eu-central-1"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("eks other stream", oneRecord(map[string]any{"cloudwatch.log.group.name": ekAuditGroup, "cloudwatch.log.stream": "authenticator-1"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("eks without stream", oneRecord(map[string]any{"cloudwatch.log.group.name": ekAuditGroup}, noAttrs, noAttrs, "", unspecified, "")),
		aws("eks with a preset category", oneRecord(eksAuditResource, noAttrs, map[string]any{"event.category": "security"}, "", unspecified, "")),
		aws("lambda group", oneRecord(map[string]any{"cloudwatch.log.group.name": "/aws/lambda/api", "aws.region": "us-east-1"}, noAttrs, noAttrs, "START", unspecified, "")),
		aws("stream without a group", oneRecord(map[string]any{"cloudwatch.log.stream": "kube-apiserver-audit-1"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("group not under eks", oneRecord(map[string]any{"cloudwatch.log.group.name": "/eks/other"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("preset auth0 source", oneRecord(map[string]any{"cloudwatch.log.group.name": ekAuditGroup}, noAttrs, map[string]any{"log.source": "auth0"}, "", plog.SeverityNumberError, "ERROR")),
		aws("preset source under a cloudtrail scope", oneRecord(noAttrs, cloudTrailScope, map[string]any{"log.source": "auth0", "rpc.method": "a.b/C"}, "", unspecified, "")),
		aws("preset severity with text", oneRecord(noAttrs, noAttrs, noAttrs, "", plog.SeverityNumberError, "boom")),
		aws("preset info with other text", oneRecord(noAttrs, noAttrs, noAttrs, "", plog.SeverityNumberInfo, "notice")),
		aws("cloudtrail error over preset error severity", oneRecord(noAttrs, cloudTrailScope, map[string]any{"rpc.method": "a/b/c", "aws.error.code": "X"}, "", plog.SeverityNumberError, "ERROR")),
		aws("cloudtrail without rpc method", oneRecord(noAttrs, cloudTrailScope, noAttrs, "x/y", unspecified, "")),
		aws("with region", oneRecord(map[string]any{"aws.region": "us-east-1"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("with region and a provider", oneRecord(map[string]any{"aws.region": "us-east-1", "cloud.provider": "x", "cloud.region": "y"}, noAttrs, noAttrs, "", unspecified, "")),
		aws("without region", oneRecord(noAttrs, noAttrs, noAttrs, "", unspecified, "")),
	}
}

func gcpCases() []parityCase {
	unspecified := plog.SeverityNumberUnspecified
	gcp := func(name string, in func(*testing.T) plog.Logs) parityCase {
		return parityCase{name, "gcp", ottlGCP, in}
	}
	typed := func(typ string) func(*testing.T) plog.Logs {
		return oneRecord(map[string]any{"gcp.resource_type": typ}, noAttrs, noAttrs, "", unspecified, "")
	}
	cases := []parityCase{
		gcp("cloud run decoder probe", decoded(gcpDecoder, cloudRunEntry(0))),
		gcp("cloud run decoder text", decoded(gcpDecoder, cloudRunEntry(2))),
		gcp("cloud run decoder json", decoded(gcpDecoder, cloudRunEntry(3))),
		gcp("audit decoder", decoded(gcpDecoder, compactFile("testdata/gcp-audit-entry.json"))),
		gcp("audit resource id", oneRecord(map[string]any{"cloud.resource_id": auditID}, noAttrs, noAttrs, "", unspecified, "")),
		gcp("audit over a preset source", oneRecord(map[string]any{"cloud.resource_id": auditID}, noAttrs, map[string]any{"log.source": "other"}, "", unspecified, "")),
		gcp("cloud run type", typed("cloud_run_revision")),
		gcp("cloud run over a preset source", oneRecord(map[string]any{"gcp.resource_type": "cloud_run_revision"}, noAttrs, map[string]any{"log.source": "other"}, "", unspecified, "")),
		gcp("lb over a preset source", oneRecord(map[string]any{"gcp.resource_type": "http_load_balancer"}, noAttrs, map[string]any{"log.source": "other"}, "", unspecified, "")),
		gcp("unmatched type", typed("gce_instance")),
		gcp("near-miss lb type", typed("http_load_balancer_v2")),
		gcp("labels and project", oneRecord(map[string]any{"gcp.label.service_name": "api", "gcp.label.location": "us-central1", "gcp.label.project_id": "p1", "gcp.project": "p2"}, noAttrs, noAttrs, "", unspecified, "")),
		gcp("project only", oneRecord(map[string]any{"gcp.project": "p2", "cloud.provider": "other"}, noAttrs, noAttrs, "", unspecified, "")),
		gcp("no labels", oneRecord(noAttrs, noAttrs, noAttrs, "", unspecified, "")),
	}
	for _, typ := range []string{"http_load_balancer", "l7_lb_rule", "https_lb_rule"} {
		cases = append(cases, gcp("lb type "+typ, typed(typ)))
	}
	return cases
}

func azureCases() []parityCase {
	return []parityCase{
		{"azure plain", "azure", ottlAzure, oneRecord(noAttrs, noAttrs, noAttrs, "m", plog.SeverityNumberUnspecified, "")},
		{"azure preset", "azure", ottlAzure, oneRecord(map[string]any{"cloud.provider": "x"}, noAttrs, map[string]any{"log.source": "x", "event.category": "y"}, "", plog.SeverityNumberError, "ERROR")},
	}
}

func allCases() []parityCase {
	return append(append(awsCases(), gcpCases()...), azureCases()...)
}

func TestSourceTagMatchesTheOTTLItReplaces(t *testing.T) {
	for _, c := range allCases() {
		t.Run(c.name, func(t *testing.T) {
			want := runChain(t, c.input, ottlMaker(t, c.fixture))
			got := withoutAdditions(runChain(t, c.input, tagMaker(t, c.provider)), want)

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
			ignoreInfoDefault(got, want)

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
			got := withoutAdditions(runChain(t, c.input, tagMaker(t, c.provider), ottlMaker(t, c.fixture)), want)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("sourcetag then OTTL differs from OTTL\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// withoutAdditions removes what the code adds on top of the OTTL it replaces, so everything else must still match:
// the resource cloud.service.name. The severity default is the other way round: the OTTL gives INFO to a record
// with no severity, the code leaves it unset (see ignoreInfoDefault).
func withoutAdditions(got, want []snapshot) []snapshot {
	out := make([]snapshot, len(got))
	for i, g := range got {
		res := maps.Clone(g.Resource)
		delete(res, "cloud.service.name")
		g.Resource = res
		out[i] = g
	}
	ignoreInfoDefault(out, want)
	return out
}

// ignoreInfoDefault sets the severity of every record of a that the OTTL gave the default INFO to the
// unset severity of the record in b, and the other way round. The code no longer defaults an unknown
// severity to INFO, and the OTTL block it replaces still does.
func ignoreInfoDefault(a, b []snapshot) {
	for i := range a {
		if i >= len(b) {
			return
		}
		switch {
		case a[i].Severity == plog.SeverityNumberUnspecified && b[i].Severity == plog.SeverityNumberInfo && b[i].Text == "INFO":
			b[i].Severity, b[i].Text = a[i].Severity, a[i].Text
		case b[i].Severity == plog.SeverityNumberUnspecified && a[i].Severity == plog.SeverityNumberInfo && a[i].Text == "INFO":
			a[i].Severity, a[i].Text = b[i].Severity, b[i].Text
		}
	}
}
