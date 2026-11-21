// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore AWSOTTL impls

package bench

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/ciscologprocessor"
	"github.com/vladistan/otelcol-custom/processor/klogprocessor"
	"github.com/vladistan/otelcol-custom/processor/logruslogprocessor"
	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
	"github.com/vladistan/otelcol-custom/processor/nginxlogprocessor"
	"github.com/vladistan/otelcol-custom/processor/postgreslogprocessor"
	"github.com/vladistan/otelcol-custom/processor/redislogprocessor"
	"github.com/vladistan/otelcol-custom/processor/sentrylogprocessor"
	"github.com/vladistan/otelcol-custom/processor/sourcetagprocessor"
	"github.com/vladistan/otelcol-custom/processor/syslogprocessor"
	"github.com/vladistan/otelcol-custom/processor/uvicornlogprocessor"
)

// The sample lines are the ones the logshape tests use, one file for each format.
const samplesDir = "../../processor/logshapeprocessor/testdata/samples"

// builder makes the processor chain of one side of a comparison.
type builder func(tb testing.TB, next consumer.Logs) consumer.Logs

// textFormat is one logshape format that arrives as a text line. block is the standalone processor the
// branch replaced; nil means the repository holds no block for the format.
type textFormat struct {
	name  string
	block builder
	// aligned is the block with the options that logshape turns on by default and the processor leaves off.
	aligned builder
	// lineAttrs also carry the line, for the processors that read it from an attribute and not from the body.
	lineAttrs []string
	// service is the service.name of the resource, for the processors that act only on their own service.
	service string
}

func factoryBlock(newFactory func() processor.Factory) builder {
	return func(tb testing.TB, next consumer.Logs) consumer.Logs {
		tb.Helper()
		f := newFactory()
		p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
		if err != nil {
			tb.Fatalf("create %s: %v", f.Type(), err)
		}
		return p
	}
}

// configuredBlock is factoryBlock with the config changed before the processor is made.
func configuredBlock(newFactory func() processor.Factory, change func(component.Config)) builder {
	return func(tb testing.TB, next consumer.Logs) consumer.Logs {
		tb.Helper()
		f := newFactory()
		cfg := f.CreateDefaultConfig()
		change(cfg)
		p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), cfg, next)
		if err != nil {
			tb.Fatalf("create %s: %v", f.Type(), err)
		}
		return p
	}
}

// textFormats lists the nine formats that had a standalone processor, then every other sample file.
var textFormats = []textFormat{
	{
		name: "nginx", block: factoryBlock(nginxlogprocessor.NewFactory),
		aligned: configuredBlock(nginxlogprocessor.NewFactory, func(c component.Config) {
			c.(*nginxlogprocessor.Config).ParseTimestamp = true
		}),
	},
	{name: "redis", block: factoryBlock(redislogprocessor.NewFactory)},
	{name: "postgres", block: factoryBlock(postgreslogprocessor.NewFactory), service: "postgres"},
	{
		name: "sentry", block: factoryBlock(sentrylogprocessor.NewFactory),
		aligned: configuredBlock(sentrylogprocessor.NewFactory, func(c component.Config) {
			cfg := c.(*sentrylogprocessor.Config)
			cfg.ParseTimestamp, cfg.PreserveOriginal = true, true
		}),
	},
	{name: "klog", block: factoryBlock(klogprocessor.NewFactory)},
	{name: "uvicorn", block: factoryBlock(uvicornlogprocessor.NewFactory), service: "uvicorn"},
	{
		name: "logrus", block: factoryBlock(logruslogprocessor.NewFactory),
		aligned: configuredBlock(logruslogprocessor.NewFactory, func(c component.Config) {
			c.(*logruslogprocessor.Config).ParseTimestamp = true
		}),
	},
	{name: "cisco", block: factoryBlock(ciscologprocessor.NewFactory), lineAttrs: []string{"original_message"}},
	{name: "syslog", block: factoryBlock(syslogprocessor.NewFactory), lineAttrs: []string{"message"}},
}

// noBlockFormats are the sample files whose format had no processor or OTTL block in the repository.
func noBlockFormats(tb testing.TB) []textFormat {
	tb.Helper()
	entries, err := os.ReadDir(samplesDir)
	if err != nil {
		tb.Fatalf("read %s: %v", samplesDir, err)
	}
	var out []textFormat
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".txt")
		if !slices.ContainsFunc(textFormats, func(f textFormat) bool { return f.name == name }) {
			out = append(out, textFormat{name: name})
		}
	}
	return out
}

func sampleLines(tb testing.TB, name string) []string {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(samplesDir, name+".txt"))
	if err != nil {
		tb.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") {
			continue // the provenance marker and other comment lines are not records
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		tb.Fatalf("%s has no lines", name)
	}
	return lines
}

// lineBatch is a batch of batchSize records that cycle the sample lines of the format.
func lineBatch(tb testing.TB, f textFormat) plog.Logs {
	tb.Helper()
	lines := sampleLines(tb, f.name)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if f.service != "" {
		rl.Resource().Attributes().PutStr("service.name", f.service)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for i := range batchSize {
		lr := sl.LogRecords().AppendEmpty()
		line := lines[i%len(lines)]
		lr.Body().SetStr(line)
		for _, key := range f.lineAttrs {
			lr.Attributes().PutStr(key, line)
		}
	}
	return ld
}

// newLogshape builds the logshape processor with the given branches; nil keeps every branch.
func newLogshape(tb testing.TB, branches []string, next consumer.Logs) consumer.Logs {
	tb.Helper()
	f := logshapeprocessor.NewFactory()
	cfg := f.CreateDefaultConfig().(*logshapeprocessor.Config)
	if branches != nil {
		cfg.Branches = branches
	}
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), cfg, next)
	if err != nil {
		tb.Fatalf("create logshape: %v", err)
	}
	return p
}

// chain joins the stages, each given the consumer after it, and returns the first.
func chain(tb testing.TB, sink consumer.Logs, stages ...builder) consumer.Logs {
	tb.Helper()
	next := sink
	for i := len(stages) - 1; i >= 0; i-- {
		next = stages[i](tb, next)
	}
	return next
}

// changedShare is the share of output records that differ from what went in: a new body, a new
// attribute or a severity. A processor that skips most of the lines would look fast for the wrong reason.
func changedShare(tb testing.TB, build builder, template plog.Logs) float64 {
	tb.Helper()
	input := plog.NewLogs()
	template.CopyTo(input)
	bodies := map[string]bool{}
	maxAttrs := 0
	forEachRecord(input, func(lr plog.LogRecord) {
		bodies[lr.Body().AsString()] = true
		maxAttrs = max(maxAttrs, lr.Attributes().Len())
	})
	sink := new(consumertest.LogsSink)
	if err := build(tb, sink).ConsumeLogs(context.Background(), input); err != nil {
		tb.Fatalf("consume: %v", err)
	}
	total, changed := 0, 0
	for _, out := range sink.AllLogs() {
		forEachRecord(out, func(lr plog.LogRecord) {
			total++
			if !bodies[lr.Body().AsString()] || lr.Attributes().Len() > maxAttrs || lr.SeverityNumber() != plog.SeverityNumberUnspecified {
				changed++
			}
		})
	}
	if total == 0 {
		tb.Fatal("no record reached the sink")
	}
	return float64(changed) / float64(total)
}

func forEachRecord(ld plog.Logs, visit func(plog.LogRecord)) {
	for i := range ld.ResourceLogs().Len() {
		scopes := ld.ResourceLogs().At(i).ScopeLogs()
		for j := range scopes.Len() {
			records := scopes.At(j).LogRecords()
			for k := range records.Len() {
				visit(records.At(k))
			}
		}
	}
}

// runCompared times the stages and reports the share of records the stages changed.
func runCompared(b *testing.B, template plog.Logs, stages ...builder) {
	b.Helper()
	build := func(tb testing.TB, sink consumer.Logs) consumer.Logs { return chain(tb, sink, stages...) }
	share := changedShare(b, build, template)
	run(b, build(b, consumertest.NewNop()), template)
	b.ReportMetric(share, "changed/record")
}

// BenchmarkLogshapeVsBlock times each format through the block it replaced and through logshape: the
// whole table as it ships (logshape-table) and the one branch alone (logshape-branch).
// Compare with: benchstat -col /impl results/logshape-raw.txt.
func BenchmarkLogshapeVsBlock(b *testing.B) {
	for _, f := range textFormats {
		template := lineBatch(b, f)
		b.Run(f.name+"/impl=block", func(b *testing.B) { runCompared(b, template, f.block) })
		if f.aligned != nil {
			b.Run(f.name+"/impl=block-aligned", func(b *testing.B) { runCompared(b, template, f.aligned) })
		}
		b.Run(f.name+"/impl=logshape-table", func(b *testing.B) {
			runCompared(b, template, func(tb testing.TB, next consumer.Logs) consumer.Logs { return newLogshape(tb, nil, next) })
		})
		b.Run(f.name+"/impl=logshape-branch", func(b *testing.B) {
			runCompared(b, template, func(tb testing.TB, next consumer.Logs) consumer.Logs {
				return newLogshape(tb, []string{f.name}, next)
			})
		})
	}
}

// BenchmarkLogshapeNoBlock times the formats that had no block in the repository, to show what they cost.
func BenchmarkLogshapeNoBlock(b *testing.B) {
	for _, f := range noBlockFormats(b) {
		template := lineBatch(b, f)
		b.Run(f.name+"/impl=logshape-table", func(b *testing.B) {
			runCompared(b, template, func(tb testing.TB, next consumer.Logs) consumer.Logs { return newLogshape(tb, nil, next) })
		})
		b.Run(f.name+"/impl=logshape-branch", func(b *testing.B) {
			runCompared(b, template, func(tb testing.TB, next consumer.Logs) consumer.Logs {
				return newLogshape(tb, []string{f.name}, next)
			})
		})
	}
}

// The OTTL block of the scrape collector for AWS logs: it tags the source, then shapes CloudTrail records.
const awsOTTLBlock = "../scrape/testdata/ottl-aws-deployed.yaml"

func newAWSOTTL(tb testing.TB, next consumer.Logs) consumer.Logs {
	tb.Helper()
	raw, err := os.ReadFile(awsOTTLBlock)
	if err != nil {
		tb.Fatal(err)
	}
	var block map[string]any
	if err := yaml.Unmarshal(raw, &block); err != nil {
		tb.Fatalf("parse %s: %v", awsOTTLBlock, err)
	}
	return newOTTLFromBlock(tb, next, block)
}

func newSourceTag(tb testing.TB, next consumer.Logs) consumer.Logs {
	tb.Helper()
	f := sourcetagprocessor.NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), &sourcetagprocessor.Config{Provider: "aws"}, next)
	if err != nil {
		tb.Fatalf("create sourcetag: %v", err)
	}
	return p
}

// cloudTrailBatch has the records the AWS logs encoder gives the scrape collector: no body, the action
// in rpc.method, an error code on one record in five.
func cloudTrailBatch(tb testing.TB) plog.Logs {
	tb.Helper()
	methods := []string{"s3.amazonaws.com/GetObject", "ec2.amazonaws.com/DescribeInstances", "sts.amazonaws.com/AssumeRole", "iam.amazonaws.com/ListRoles"}
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("aws.region", "eu-west-1")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().Attributes().PutStr("encoding.format", "aws.cloudtrail")
	for i := range batchSize {
		lr := sl.LogRecords().AppendEmpty()
		lr.Attributes().PutStr("rpc.method", methods[i%len(methods)])
		lr.Attributes().PutStr("user.name", "build-role")
		if i%5 == 0 {
			lr.Attributes().PutStr("aws.error.code", "AccessDenied")
		}
	}
	return ld
}

// BenchmarkLogshapeCloudTrail compares the OTTL block with the source tagger and logshape that replace it.
func BenchmarkLogshapeCloudTrail(b *testing.B) {
	template := cloudTrailBatch(b)
	b.Run("impl=ottl", func(b *testing.B) { runCompared(b, template, newAWSOTTL) })
	b.Run("impl=sourcetag+logshape-table", func(b *testing.B) {
		runCompared(b, template, newSourceTag, func(tb testing.TB, next consumer.Logs) consumer.Logs { return newLogshape(tb, nil, next) })
	})
	b.Run("impl=sourcetag+logshape-branch", func(b *testing.B) {
		runCompared(b, template, newSourceTag, func(tb testing.TB, next consumer.Logs) consumer.Logs {
			return newLogshape(tb, []string{"cloudtrail"}, next)
		})
	})
}

// implementations are the chains a format is timed through.
func implementations(f textFormat) map[string]builder {
	impls := map[string]builder{
		"logshape-table":  func(tb testing.TB, next consumer.Logs) consumer.Logs { return newLogshape(tb, nil, next) },
		"logshape-branch": func(tb testing.TB, next consumer.Logs) consumer.Logs { return newLogshape(tb, []string{f.name}, next) },
	}
	if f.block != nil {
		impls["block"] = f.block
	}
	if f.aligned != nil {
		impls["block-aligned"] = f.aligned
	}
	return impls
}

func TestLogshapeBench_EveryImplementationDoesTheWork(t *testing.T) {
	for _, f := range append(slices.Clone(textFormats), noBlockFormats(t)...) {
		t.Run(f.name, func(t *testing.T) {
			template := lineBatch(t, f)
			for name, build := range implementations(f) {
				t.Run(name, func(t *testing.T) {
					share := changedShare(t, build, template)

					t.Logf("changed share %.2f", share)
					if share == 0 {
						t.Error("no record changed: the timing would measure nothing")
					}
				})
			}
		})
	}
}
