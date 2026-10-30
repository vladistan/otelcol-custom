// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/healthprobesampler"
	"github.com/vladistan/otelcol-custom/processor/nginxlogprocessor"
)

const batchSize = 1000

// Compare with: benchstat -col /impl results/<file>.txt.

func journaldBatch(b *testing.B, units int) plog.Logs {
	b.Helper()
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for i := 0; i < batchSize; i++ {
		u := i % units
		body := map[string]any{
			"_HOSTNAME":         fmt.Sprintf("host-%d.example.com", u),
			"_MACHINE_ID":       fmt.Sprintf("%032d", u),
			"_PID":              fmt.Sprintf("%d", 1000+u),
			"_COMM":             fmt.Sprintf("comm-%d", u),
			"_SYSTEMD_UNIT":     fmt.Sprintf("unit-%d.service", u),
			"PRIORITY":          fmt.Sprintf("%d", 3+i%4),
			"MESSAGE":           "synthetic journald message",
			"SYSLOG_IDENTIFIER": "synthetic",
			"_TRANSPORT":        "journal",
		}
		if err := sl.LogRecords().AppendEmpty().Body().SetEmptyMap().FromRaw(body); err != nil {
			b.Fatalf("set body: %v", err)
		}
	}
	return ld
}

func nginxBatch(b *testing.B) plog.Logs {
	b.Helper()
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for i := 0; i < batchSize; i++ {
		var line string
		switch i % 10 {
		case 0, 1, 2:
			line = `10.0.0.1 - - [31/Dec/2025:00:30:00 +0000] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.29" "-"`
		default:
			line = fmt.Sprintf(`10.0.0.%d - - [31/Dec/2025:00:30:00 +0000] "POST /api/2/envelope/ HTTP/1.1" 200 41 "-" "sentry.go/0.40.0" "-"`, i%200)
		}
		sl.LogRecords().AppendEmpty().Body().SetStr(line)
	}
	return ld
}

// run times proc over fresh copies of template; copying is outside the timer.
func run(b *testing.B, proc consumer.Logs, template plog.Logs) {
	b.Helper()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ld := plog.NewLogs()
		template.CopyTo(ld)
		b.StartTimer()
		if err := proc.ConsumeLogs(ctx, ld); err != nil {
			b.Fatalf("consume: %v", err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*batchSize), "ns/record")
}

func BenchmarkJournald(b *testing.B) {
	for _, mix := range []struct {
		name  string
		units int
	}{{"single", 1}, {"mixed", 5}} {
		template := journaldBatch(b, mix.units)
		b.Run(mix.name+"/impl=ottl", func(b *testing.B) {
			run(b, newOTTL(b, consumertest.NewNop()), template)
		})
		b.Run(mix.name+"/impl=journald", func(b *testing.B) {
			run(b, newJournald(b, consumertest.NewNop()), template)
		})
	}
}

func BenchmarkNginxHealthProbe(b *testing.B) {
	template := nginxBatch(b)
	nop := consumertest.NewNop()

	b.Run("chain/impl=parser", func(b *testing.B) {
		run(b, newNginx(b, nop), template)
	})
	b.Run("chain/impl=parser+healthprobe", func(b *testing.B) {
		hp := newHealthProbe(b, nop)
		run(b, newNginx(b, hp), template)
	})
}

func k8sBatch(b *testing.B) plog.Logs {
	b.Helper()
	cases := loadK8sCases(b)
	ld := plog.NewLogs()
	sl := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	for i := 0; i < batchSize; i++ {
		if err := sl.LogRecords().AppendEmpty().Body().SetEmptyMap().FromRaw(cases[i%len(cases)].Body); err != nil {
			b.Fatalf("set body: %v", err)
		}
	}
	return ld
}

func BenchmarkK8sEvent(b *testing.B) {
	template := k8sBatch(b)
	nop := consumertest.NewNop()

	b.Run("impl=ottl", func(b *testing.B) {
		run(b, newK8sOTTL(b, nop), template)
	})
	b.Run("impl=k8sevent", func(b *testing.B) {
		run(b, newK8sEvent(b, nop, nil), template)
	})
}

func newNginx(b *testing.B, next consumer.Logs) processor.Logs {
	b.Helper()
	f := nginxlogprocessor.NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		b.Fatalf("create nginxlogprocessor: %v", err)
	}
	return p
}

func newHealthProbe(b *testing.B, next consumer.Logs) processor.Logs {
	b.Helper()
	f := healthprobesampler.NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		b.Fatalf("create healthprobesampler: %v", err)
	}
	return p
}
