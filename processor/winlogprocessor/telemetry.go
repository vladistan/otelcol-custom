// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const scopeName = "github.com/vladistan/otelcol-custom/processor/winlogprocessor"

// telemetry holds the read and drop counters on the component MeterProvider.
type telemetry struct {
	read    metric.Int64Counter
	dropped metric.Int64Counter
}

func newTelemetry(ts component.TelemetrySettings) (*telemetry, error) {
	meter := ts.MeterProvider.Meter(scopeName)
	read, err := meter.Int64Counter("otelcol_winlog_events_read",
		metric.WithDescription("Event log records seen, per channel, before drop rules run."),
		metric.WithUnit("{event}"))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter("otelcol_winlog_events_dropped",
		metric.WithDescription("Event log records dropped, per channel and first matching rule."),
		metric.WithUnit("{event}"))
	if err != nil {
		return nil, err
	}
	return &telemetry{read: read, dropped: dropped}, nil
}

type readTally struct {
	channel string
	n       int64
}

type dropTally struct {
	channel, rule string
	n             int64
}

// tally batches one ConsumeLogs call's counts so each distinct attribute set
// costs one Add instead of one per record.
type tally struct {
	reads []readTally
	drops []dropTally
}

func (t *tally) addRead(channel string) {
	for i := range t.reads {
		if t.reads[i].channel == channel {
			t.reads[i].n++
			return
		}
	}
	t.reads = append(t.reads, readTally{channel: channel, n: 1})
}

func (t *tally) addDrop(channel, rule string) {
	for i := range t.drops {
		if t.drops[i].channel == channel && t.drops[i].rule == rule {
			t.drops[i].n++
			return
		}
	}
	t.drops = append(t.drops, dropTally{channel: channel, rule: rule, n: 1})
}

func (tel *telemetry) flush(ctx context.Context, t *tally) {
	for _, r := range t.reads {
		tel.read.Add(ctx, r.n, metric.WithAttributes(attribute.String("channel", r.channel)))
	}
	for _, d := range t.drops {
		tel.dropped.Add(ctx, d.n, metric.WithAttributes(
			attribute.String("channel", d.channel), attribute.String("rule", d.rule)))
	}
}
