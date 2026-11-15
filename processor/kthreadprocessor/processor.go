// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	executableNameAttr = "process.executable.name"
	scrapeGap          = time.Second
)

type processorImpl struct {
	next          consumer.Metrics
	patterns      []*regexp.Regexp
	aggregateName string
}

func newProcessor(cfg *Config, next consumer.Metrics) (*processorImpl, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &processorImpl{next: next, aggregateName: cfg.AggregateName}
	for _, pattern := range cfg.Patterns {
		re, err := regexp.Compile(globToRegexp(pattern))
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", pattern, err)
		}
		p.patterns = append(p.patterns, re)
	}
	return p, nil
}

// globToRegexp anchors the pattern and turns only * and ? into wildcards.
func globToRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^(?s:")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString(")$")
	return b.String()
}

func (p *processorImpl) matches(name string) bool {
	for _, re := range p.patterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

func (*processorImpl) Start(context.Context, component.Host) error { return nil }

func (*processorImpl) Shutdown(context.Context) error { return nil }

func (*processorImpl) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeMetrics merges every kernel-thread resource into one aggregate resource,
// placed after the unchanged resources.
func (p *processorImpl) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	agg := newAggregate(p.scrapeEnds(md))
	md.ResourceMetrics().RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		if !p.isKernelThread(rm.Resource().Attributes()) {
			return false
		}
		agg.add(rm)
		return true
	})
	agg.appendTo(md, p.aggregateName)
	return p.next.ConsumeMetrics(ctx, md)
}

// scrapeEnds maps every point timestamp of the matching resources to the latest
// timestamp of its scrape. The process scraper stamps each process when it reads
// it, so one scrape spans many microseconds; timestamps less than scrapeGap
// apart belong to one scrape.
func (p *processorImpl) scrapeEnds(md pmetric.Metrics) map[pcommon.Timestamp]pcommon.Timestamp {
	seen := map[pcommon.Timestamp]struct{}{}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		if !p.isKernelThread(rms.At(i).Resource().Attributes()) {
			continue
		}
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				for _, ts := range pointTimes(ms.At(k)) {
					seen[ts] = struct{}{}
				}
			}
		}
	}
	times := slices.Sorted(maps.Keys(seen))
	ends := make(map[pcommon.Timestamp]pcommon.Timestamp, len(times))
	for start := 0; start < len(times); {
		end := start
		for end+1 < len(times) && times[end+1]-times[end] < pcommon.Timestamp(scrapeGap) {
			end++
		}
		for _, ts := range times[start : end+1] {
			ends[ts] = times[end]
		}
		start = end + 1
	}
	return ends
}

func pointTimes(m pmetric.Metric) []pcommon.Timestamp {
	var dps pmetric.NumberDataPointSlice
	switch m.Type() {
	case pmetric.MetricTypeSum:
		dps = m.Sum().DataPoints()
	case pmetric.MetricTypeGauge:
		dps = m.Gauge().DataPoints()
	default:
		return nil
	}
	out := make([]pcommon.Timestamp, 0, dps.Len())
	for i := 0; i < dps.Len(); i++ {
		out = append(out, dps.At(i).Timestamp())
	}
	return out
}

// isKernelThread is false for a resource with no string executable name.
func (p *processorImpl) isKernelThread(attrs pcommon.Map) bool {
	v, ok := attrs.Get(executableNameAttr)
	return ok && v.Type() == pcommon.ValueTypeStr && p.matches(v.Str())
}
