// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipextractorprocessor

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type logsProcessor struct {
	config       *Config
	logger       *zap.Logger
	nextConsumer consumer.Logs
}

func (p *logsProcessor) Start(_ context.Context, _ component.Host) error {
	p.logger.Info("IP extractor processor started",
		zap.String("target_attribute", p.config.TargetAttribute),
		zap.Bool("search_body", p.config.SearchBody),
		zap.Strings("attributes_to_search", p.config.AttributesToSearch),
		zap.Bool("extract_all", p.config.ExtractAll),
		zap.Bool("extract_ipv6", p.config.ExtractIPv6),
	)
	return nil
}

func (p *logsProcessor) Shutdown(_ context.Context) error {
	return nil
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				p.processLogRecord(lr)
			}
		}
	}
	return p.nextConsumer.ConsumeLogs(ctx, ld)
}

func (p *logsProcessor) processLogRecord(lr plog.LogRecord) {
	var allIPs []string

	// Search body if configured (handles strings, maps, and slices)
	if p.config.SearchBody {
		bodyIPs := p.extractFromValue(lr.Body())
		allIPs = append(allIPs, bodyIPs...)
	}

	// Search whitelisted attributes only
	if len(p.config.AttributesToSearch) > 0 {
		attrIPs := p.extractFromAttributes(lr.Attributes())
		allIPs = append(allIPs, attrIPs...)
	}

	// Remove duplicates while preserving order
	uniqueIPs := uniqueStrings(allIPs)

	// Set the attribute on the log record if we found IPs
	if len(uniqueIPs) > 0 {
		if p.config.ExtractAll {
			// Store all IPs as comma-separated
			lr.Attributes().PutStr(p.config.TargetAttribute, strings.Join(uniqueIPs, ","))
		} else {
			// Store only the first IP
			lr.Attributes().PutStr(p.config.TargetAttribute, uniqueIPs[0])
		}
	}
}

func (p *logsProcessor) extractFromValue(val pcommon.Value) []string {
	switch val.Type() {
	case pcommon.ValueTypeStr:
		return ExtractIPs(val.Str(), p.config.ExtractIPv6)
	case pcommon.ValueTypeMap:
		return p.extractFromMap(val.Map())
	case pcommon.ValueTypeSlice:
		return p.extractFromSlice(val.Slice())
	default:
		return nil
	}
}

func (p *logsProcessor) extractFromMap(m pcommon.Map) []string {
	var ips []string
	m.Range(func(_ string, v pcommon.Value) bool {
		ips = append(ips, p.extractFromValue(v)...)
		return true
	})
	return ips
}

func (p *logsProcessor) extractFromSlice(s pcommon.Slice) []string {
	var ips []string
	for i := 0; i < s.Len(); i++ {
		ips = append(ips, p.extractFromValue(s.At(i))...)
	}
	return ips
}

// extractFromAttributes searches only the whitelisted attributes for IPs.
// Handles missing attributes gracefully (attrs.Get returns ok=false).
func (p *logsProcessor) extractFromAttributes(attrs pcommon.Map) []string {
	var ips []string
	for _, attrName := range p.config.AttributesToSearch {
		if val, ok := attrs.Get(attrName); ok {
			// extractFromValue handles all value types safely
			ips = append(ips, p.extractFromValue(val)...)
		}
	}
	return ips
}

// uniqueStrings removes duplicates while preserving order
func uniqueStrings(input []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(input))
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
