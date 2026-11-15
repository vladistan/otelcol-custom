// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/regex"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/severity"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/timeparser"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/add"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/filter"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/move"
	_ "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/transformer/remove"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/pipeline"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"

	"github.com/vladistan/otelcol-custom/processor/tightvncprocessor"
)

const noiseCorpusBytes = 4 << 20

// noiseShapes are the debug lines a LogLevel 9 server writes; every one has to be dropped.
var noiseShapes = []string{
	" : Control client sends process ID",
	"   Recieved control message ID 20, size 0",
	" : Control client requests tray icon visibility flag",
	" : Control client requests server info",
	" : Control client requests client list",
	"   process memory usage: 12734464",
	"   new client, process memory usage: 12832768",
	"   Shared flag = 1",
	"   Initial view-only state = 0",
	"   RFB initialization phase 1 completed",
	" : Requested to start up extra RFB servers",
	" : Considering to reload extra RFB servers",
	" : Same Extra Ports configuration = 0, enough servers = 1",
	"   Timeout on acquire frame",
	"   DesktopSrvDispatcher reading code",
	"   View port coordinates: 0 0 1920 1080",
	"   Constrained to the FrameBuffer dimension",
	"   Destination dirty rect: 0 0 1920 1080",
	"   update detected",
	"   Acquire frame for output",
	"   get frame buffer properties",
	"   Time between request",
	"   UpdateFilter::filter",
	"   WinVideoRegionUpdaterImpl::execute",
}

// stopgapOperators is the operator chain of the filelog/tightvnc stopgap the processor replaces.
const stopgapOperators = `
operators:
  - type: regex_parser
    regex: '^\s*\[\s*(?P<pid>\d+)\s*/\s*(?P<tid>\d+)\s*\]\s+(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}):(?P<ms>\d{3})\s*:?\s+(?:(?P<marker>[+!-])\s+)?(?P<msg>.*?)\s*$'
    on_error: drop_quiet
  - type: add
    field: attributes.ts
    value: EXPR(attributes.ts + "." + attributes.ms)
  - type: time_parser
    parse_from: attributes.ts
    layout: '%Y-%m-%d %H:%M:%S.%L'
    location: Local
    on_error: send_quiet
  - type: severity_parser
    parse_from: attributes.marker
    mapping:
      error: '!'
      info: ['+', '-']
    on_error: send_quiet
  - type: filter
    expr: 'attributes.msg matches "^(Control client requests|Recieved control message|process memory usage)"'
  - type: filter
    expr: 'attributes.msg matches "^(WinVideoRegionUpdaterImpl|DesktopSrvDispatcher|Timeout on acquire frame|View port|Constrained)"'
  - type: filter
    expr: 'attributes.msg matches "^(Destination dirty rect|update detected|Acquire frame|get frame buffer|Time between|UpdateFilter)"'
  - type: filter
    expr: |-
      not ((attributes.marker ?? "") != ""
        or attributes.msg matches "(?i)incoming rfb|client #|authenticat|disconnect|connected"
        or attributes.msg matches "(?i)closing|closed|reject|denied|refus|fail|error|exception|warning|password")
  - type: remove
    field: attributes.ts
  - type: remove
    field: attributes.ms
  - type: move
    from: attributes.msg
    to: body
`

// noiseCorpus returns about 4 MiB of noise lines with varying thread ids and times.
func noiseCorpus() []string {
	var lines []string
	size := 0
	for i := 0; size < noiseCorpusBytes; i++ {
		line := fmt.Sprintf("[ 6020/%5d] 2026-11-11 11:%02d:%02d:%03d%s",
			1000+i%40, i/1000%60, i/10%60, i%1000, noiseShapes[i%len(noiseShapes)])
		lines = append(lines, line)
		size += len(line)
	}
	return lines
}

func tightvncBatches(lines []string) []plog.Logs {
	var batches []plog.Logs
	for start := 0; start < len(lines); start += batchSize {
		ld := plog.NewLogs()
		records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
		for _, line := range lines[start:min(start+batchSize, len(lines))] {
			records.AppendEmpty().Body().SetStr(line)
		}
		batches = append(batches, ld)
	}
	return batches
}

func newTightvnc(tb testing.TB, next consumer.Logs) consumer.Logs {
	tb.Helper()
	f := tightvncprocessor.NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		tb.Fatalf("create tightvncprocessor: %v", err)
	}
	return p
}

// stopgapChain builds the stanza operators and returns the first one and the count of entries
// that leave the last one. A drain goroutine keeps the fake output from blocking.
func stopgapChain(tb testing.TB) (first operator.Operator, out *atomic.Int64) {
	tb.Helper()
	var tree map[string]any
	if err := yaml.Unmarshal([]byte(stopgapOperators), &tree); err != nil {
		tb.Fatalf("parse stopgap operators: %v", err)
	}
	var holder struct {
		Operators []operator.Config `mapstructure:"operators"`
	}
	if err := confmap.NewFromStringMap(tree).Unmarshal(&holder); err != nil {
		tb.Fatalf("unmarshal stopgap operators: %v", err)
	}
	fake := testutil.NewFakeOutput(tb)
	out = new(atomic.Int64)
	go func() {
		for range fake.Received {
			out.Add(1)
		}
	}()
	built, err := pipeline.Config{Operators: holder.Operators, DefaultOutput: fake}.
		Build(componenttest.NewNopTelemetrySettings())
	if err != nil {
		tb.Fatalf("build stopgap pipeline: %v", err)
	}
	if err := built.Start(testutil.NewUnscopedMockPersister()); err != nil {
		tb.Fatalf("start stopgap pipeline: %v", err)
	}
	tb.Cleanup(func() { _ = built.Stop() })
	return built.Operators()[0], out
}

func feedStopgap(tb testing.TB, first operator.Operator, lines []string) {
	tb.Helper()
	ctx := context.Background()
	for _, line := range lines {
		e := entry.New()
		e.Body = line
		if err := first.Process(ctx, e); err != nil {
			tb.Fatalf("stopgap process: %v", err)
		}
	}
}

// Both implementations have to drop the whole corpus, or the timings compare different work.
func TestTightvncBench_BothChainsDropTheWholeCorpus(t *testing.T) {
	lines := noiseCorpus()
	sink := new(consumertest.LogsSink)
	proc := newTightvnc(t, sink)
	for _, batch := range tightvncBatches(lines) {
		if err := proc.ConsumeLogs(context.Background(), batch); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	if got := sink.LogRecordCount(); got != 0 {
		t.Errorf("tightvnc kept %d of %d noise lines, want 0", got, len(lines))
	}

	first, out := stopgapChain(t)
	feedStopgap(t, first, lines)
	if got := out.Load(); got != 0 {
		t.Errorf("stopgap chain kept %d of %d noise lines, want 0", got, len(lines))
	}
}

func BenchmarkTightvnc(b *testing.B) {
	lines := noiseCorpus()
	records := float64(len(lines))

	b.Run("impl=stanza-chain", func(b *testing.B) {
		first, _ := stopgapChain(b)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			feedStopgap(b, first, lines)
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*records), "ns/record")
	})

	b.Run("impl=tightvnc", func(b *testing.B) {
		templates := tightvncBatches(lines)
		proc := newTightvnc(b, consumertest.NewNop())
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			batches := make([]plog.Logs, len(templates))
			for j, template := range templates {
				batches[j] = plog.NewLogs()
				template.CopyTo(batches[j])
			}
			b.StartTimer()
			for _, ld := range batches {
				if err := proc.ConsumeLogs(ctx, ld); err != nil {
					b.Fatalf("consume: %v", err)
				}
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*records), "ns/record")
	})
}
