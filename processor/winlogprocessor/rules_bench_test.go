// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

// twentyRules never match the benchmark events, so each record pays for every rule.
func twentyRules() []DropRule {
	rules := make([]DropRule, 20)
	for i := range rules {
		rules[i] = DropRule{
			Name:      fmt.Sprintf("rule-%d", i),
			Channels:  []string{"Security"},
			EventIDs:  []string{fmt.Sprint(9000 + i), fmt.Sprintf("%d-%d", 100+i, 105+i)},
			Providers: []string{"Provider-" + fmt.Sprint(i)},
		}
	}
	return rules
}

func benchBatch(n int) plog.Logs {
	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for i := 0; i < n; i++ {
		testEvent{channel: "Security", id: 4625, provider: "Microsoft-Windows-Security-Auditing", xmlLevel: "0"}.
			toLogRecord().CopyTo(records.AppendEmpty())
	}
	return logs
}

func benchmarkConsume(b *testing.B, rules []DropRule) {
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, &Config{DropRules: rules}, consumertest.NewNop())
	if err != nil {
		b.Fatalf("create processor: %v", err)
	}
	const batchSize = 1000
	template := benchBatch(batchSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		logs := plog.NewLogs()
		template.CopyTo(logs)
		b.StartTimer()
		if err := proc.ConsumeLogs(context.Background(), logs); err != nil {
			b.Fatalf("consume: %v", err)
		}
	}
}

func BenchmarkConsumeNoRules(b *testing.B)      { benchmarkConsume(b, nil) }
func BenchmarkConsumeTwentyRules(b *testing.B)  { benchmarkConsume(b, twentyRules()) }
func BenchmarkConsumeDefaultRules(b *testing.B) { benchmarkConsume(b, DefaultDropRules()) }
