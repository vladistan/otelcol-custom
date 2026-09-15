// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package assetdbprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

// Attribute names for enrichment (custom conventions following OTel patterns)
const (
	attrClientHostname     = "client.hostname"
	attrHostGeoDescription = "host.geo.description"
)

// logsProcessor enriches logs with asset database information.
type logsProcessor struct {
	config          *Config
	nextConsumer    consumer.Logs
	logger          *zap.Logger
	db              *assetDB
	sourceAttribute string
}

func newLogsProcessor(set processor.Settings, cfg *Config, next consumer.Logs) (*logsProcessor, error) {
	sourceAttr := cfg.SourceAttribute
	if sourceAttr == "" {
		sourceAttr = defaultSourceAttribute
	}

	return &logsProcessor{
		config:          cfg,
		nextConsumer:    next,
		logger:          set.Logger,
		sourceAttribute: sourceAttr,
	}, nil
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) Start(ctx context.Context, host component.Host) error {
	db, err := loadAssetDB(p.config.DatabasePath)
	if err != nil {
		return fmt.Errorf("failed to load asset database: %w", err)
	}
	p.db = db
	p.logger.Info("Asset database loaded", zap.Int("assets", db.size()), zap.String("path", p.config.DatabasePath))
	return nil
}

func (p *logsProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				p.enrichLogRecord(lr.Attributes())
			}
		}
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

// enrichLogRecord adds asset attributes to log record attributes if MAC is found.
func (p *logsProcessor) enrichLogRecord(attrs pcommon.Map) {
	macVal, ok := attrs.Get(p.sourceAttribute)
	if !ok {
		return
	}

	mac := macVal.Str()
	if mac == "" {
		return
	}

	asset := p.db.lookup(mac)
	if asset == nil {
		p.logger.Debug("Asset not found for MAC", zap.String("mac", mac))
		return
	}

	// Add enrichment attributes
	if asset.Hostname != "" {
		attrs.PutStr(attrClientHostname, asset.Hostname)
	}
	if asset.Location != "" {
		attrs.PutStr(attrHostGeoDescription, asset.Location)
	}
}

// metricsProcessor enriches metrics with asset database information.
type metricsProcessor struct {
	config          *Config
	nextConsumer    consumer.Metrics
	logger          *zap.Logger
	db              *assetDB
	sourceAttribute string
}

func newMetricsProcessor(set processor.Settings, cfg *Config, next consumer.Metrics) (*metricsProcessor, error) {
	sourceAttr := cfg.SourceAttribute
	if sourceAttr == "" {
		sourceAttr = defaultSourceAttribute
	}

	return &metricsProcessor{
		config:          cfg,
		nextConsumer:    next,
		logger:          set.Logger,
		sourceAttribute: sourceAttr,
	}, nil
}

func (p *metricsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *metricsProcessor) Start(ctx context.Context, host component.Host) error {
	db, err := loadAssetDB(p.config.DatabasePath)
	if err != nil {
		return fmt.Errorf("failed to load asset database: %w", err)
	}
	p.db = db
	p.logger.Info("Asset database loaded", zap.Int("assets", db.size()), zap.String("path", p.config.DatabasePath))
	return nil
}

func (p *metricsProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *metricsProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		p.enrichResource(rm.Resource().Attributes())
	}
	return p.nextConsumer.ConsumeMetrics(ctx, md)
}

// enrichResource adds asset attributes to the resource if MAC is found.
func (p *metricsProcessor) enrichResource(attrs pcommon.Map) {
	macVal, ok := attrs.Get(p.sourceAttribute)
	if !ok {
		return
	}

	mac := macVal.Str()
	if mac == "" {
		return
	}

	asset := p.db.lookup(mac)
	if asset == nil {
		p.logger.Debug("Asset not found for MAC", zap.String("mac", mac))
		return
	}

	// Add enrichment attributes
	if asset.Hostname != "" {
		attrs.PutStr(attrClientHostname, asset.Hostname)
	}
	if asset.Location != "" {
		attrs.PutStr(attrHostGeoDescription, asset.Location)
	}
}

// tracesProcessor enriches traces with asset database information.
type tracesProcessor struct {
	config          *Config
	nextConsumer    consumer.Traces
	logger          *zap.Logger
	db              *assetDB
	sourceAttribute string
}

func newTracesProcessor(set processor.Settings, cfg *Config, next consumer.Traces) (*tracesProcessor, error) {
	sourceAttr := cfg.SourceAttribute
	if sourceAttr == "" {
		sourceAttr = defaultSourceAttribute
	}

	return &tracesProcessor{
		config:          cfg,
		nextConsumer:    next,
		logger:          set.Logger,
		sourceAttribute: sourceAttr,
	}, nil
}

func (p *tracesProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *tracesProcessor) Start(ctx context.Context, host component.Host) error {
	db, err := loadAssetDB(p.config.DatabasePath)
	if err != nil {
		return fmt.Errorf("failed to load asset database: %w", err)
	}
	p.db = db
	p.logger.Info("Asset database loaded", zap.Int("assets", db.size()), zap.String("path", p.config.DatabasePath))
	return nil
}

func (p *tracesProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *tracesProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		rs := td.ResourceSpans().At(i)
		p.enrichResource(rs.Resource().Attributes())
	}
	return p.nextConsumer.ConsumeTraces(ctx, td)
}

// enrichResource adds asset attributes to the resource if MAC is found.
func (p *tracesProcessor) enrichResource(attrs pcommon.Map) {
	macVal, ok := attrs.Get(p.sourceAttribute)
	if !ok {
		return
	}

	mac := macVal.Str()
	if mac == "" {
		return
	}

	asset := p.db.lookup(mac)
	if asset == nil {
		p.logger.Debug("Asset not found for MAC", zap.String("mac", mac))
		return
	}

	// Add enrichment attributes
	if asset.Hostname != "" {
		attrs.PutStr(attrClientHostname, asset.Hostname)
	}
	if asset.Location != "" {
		attrs.PutStr(attrHostGeoDescription, asset.Location)
	}
}
