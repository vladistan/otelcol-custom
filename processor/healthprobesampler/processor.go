// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package healthprobesampler

import (
	"context"
	"strconv"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/otel/metric"

	"github.com/vladistan/otelcol-custom/processor/internal/probesample"
)

const (
	probeBody    = "HealthProbe"
	logTypeKey   = "log.type"
	logTypeProbe = "health_probe"
)

type processorImpl struct {
	cfg     *Config
	sampler *probesample.Sampler
	next    consumer.Logs
	kept    metric.Int64Counter
	dropped metric.Int64Counter
}

// newProcessor builds the processor; draw is the random source, nil for math/rand/v2.
func newProcessor(cfg *Config, telemetry component.TelemetrySettings, next consumer.Logs, draw func() float64) (*processorImpl, error) {
	meter := telemetry.MeterProvider.Meter("healthprobesampler")
	kept, err := meter.Int64Counter("healthprobe.kept", metric.WithDescription("Health probes kept"))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter("healthprobe.dropped", metric.WithDescription("Health probes dropped"))
	if err != nil {
		return nil, err
	}
	return &processorImpl{
		cfg:     cfg,
		sampler: probesample.New(cfg.Config, draw),
		next:    next,
		kept:    kept,
		dropped: dropped,
	}, nil
}

func (p *processorImpl) Start(context.Context, component.Host) error { return nil }

func (p *processorImpl) Shutdown(context.Context) error { return nil }

func (p *processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *processorImpl) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	if !p.cfg.Enabled {
		return p.next.ConsumeLogs(ctx, ld)
	}

	var kept, dropped int64
	ld.ResourceLogs().RemoveIf(func(rl plog.ResourceLogs) bool {
		rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool {
			sl.LogRecords().RemoveIf(func(lr plog.LogRecord) bool {
				if !p.isProbe(lr) {
					return false
				}
				if !p.sampler.Keep() {
					dropped++
					return true
				}
				lr.Body().SetStr(probeBody)
				lr.Attributes().PutStr(logTypeKey, logTypeProbe)
				kept++
				return false
			})
			return sl.LogRecords().Len() == 0
		})
		return rl.ScopeLogs().Len() == 0
	})

	if kept > 0 {
		p.kept.Add(ctx, kept)
	}
	if dropped > 0 {
		p.dropped.Add(ctx, dropped)
	}
	if ld.LogRecordCount() == 0 {
		return nil
	}
	return p.next.ConsumeLogs(ctx, ld)
}

func (p *processorImpl) isProbe(lr plog.LogRecord) bool {
	attrs := lr.Attributes()
	status, ok := statusCode(attrs, p.cfg.StatusAttribute)
	if !ok {
		return false
	}
	return p.sampler.Classify(status, str(attrs, p.cfg.PathAttribute), str(attrs, p.cfg.AgentAttribute))
}

func str(attrs pcommon.Map, key string) string {
	if v, ok := attrs.Get(key); ok && v.Type() == pcommon.ValueTypeStr {
		return v.Str()
	}
	return ""
}

// statusCode reads an int or numeric-string attribute.
func statusCode(attrs pcommon.Map, key string) (int, bool) {
	v, ok := attrs.Get(key)
	if !ok {
		return 0, false
	}
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return int(v.Int()), true
	case pcommon.ValueTypeStr:
		n, err := strconv.Atoi(v.Str())
		return n, err == nil
	}
	return 0, false
}
