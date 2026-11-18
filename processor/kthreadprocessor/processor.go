// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	executableNameAttr = "process.executable.name"
	parentPidAttr      = "process.parent_pid"
	pidAttr            = "process.pid"
	commandLineAttr    = "process.command_line"
	executablePathAttr = "process.executable.path"
	osTypeAttr         = "os.type"
	scrapeGap          = time.Second

	// kthreaddPid is the pid of the kernel thread that starts all others.
	kthreaddPid = 2
	// commLen is the longest process name the kernel keeps (TASK_COMM_LEN is 16
	// with the terminating byte).
	commLen = 15
)

type processorImpl struct {
	next          consumer.Metrics
	patterns      []*regexp.Regexp
	cutNames      map[string]struct{}
	aggregateName string
	kernelParent  bool
}

func newProcessor(cfg *Config, next consumer.Metrics) (*processorImpl, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &processorImpl{
		next:          next,
		aggregateName: cfg.AggregateName,
		kernelParent:  cfg.KernelParent,
		cutNames:      map[string]struct{}{},
	}
	for _, pattern := range cfg.Patterns {
		if cut, ok := cutName(pattern); ok {
			p.cutNames[cut] = struct{}{}
		}
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

// cutName is the first 15 bytes of a literal pattern, the name the kernel reports.
// It exists only for a longer pattern with no wildcard in those bytes and no
// multibyte character split by the cut; a cut name that is not valid text never
// arrives as an attribute.
func cutName(pattern string) (string, bool) {
	if len(pattern) <= commLen || strings.ContainsAny(pattern[:commLen], "*?") {
		return "", false
	}
	if cut := pattern[:commLen]; utf8.ValidString(cut) {
		return cut, true
	}
	return "", false
}

// matchesPattern is true when a glob matches the whole name.
func (p *processorImpl) matchesPattern(name string) bool {
	for _, re := range p.patterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// matchesCut is true when the name is the 15 bytes the kernel keeps of a literal
// pattern longer than that.
func (p *processorImpl) matchesCut(name string) bool {
	if len(name) != commLen {
		return false
	}
	_, ok := p.cutNames[name]
	return ok
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

// isKernelThread is true for a resource whose executable name matches a pattern
// and, with kernel_parent, for one the kernel started. A cut name matches only a
// resource with no command line and no executable path, as a kernel thread has
// none; a real process of that name has one.
func (p *processorImpl) isKernelThread(attrs pcommon.Map) bool {
	if v, ok := attrs.Get(executableNameAttr); ok && v.Type() == pcommon.ValueTypeStr {
		if p.matchesPattern(v.Str()) || (p.matchesCut(v.Str()) && !hasProcessIdentity(attrs)) {
			return true
		}
	}
	return p.kernelParent && startedByTheKernel(attrs)
}

// hasProcessIdentity is true when the resource carries a command line or an
// executable path. A value that is not a string counts as present.
func hasProcessIdentity(attrs pcommon.Map) bool {
	return hasValue(attrs, commandLineAttr) || hasValue(attrs, executablePathAttr)
}

func hasValue(attrs pcommon.Map, key string) bool {
	v, ok := attrs.Get(key)
	return ok && (v.Type() != pcommon.ValueTypeStr || v.Str() != "")
}

// startedByTheKernel is true for a Linux resource with no command line and no
// executable path that is a child of pid 2, or is pid 2 itself (kthreadd, which has
// no parent or parent 0). An absent command line alone is no evidence: operators
// switch it off, and a container has a pid 2 of its own, which has a live parent.
func startedByTheKernel(attrs pcommon.Map) bool {
	if hasProcessIdentity(attrs) || !isLinux(attrs) {
		return false
	}
	return pidIs(attrs, parentPidAttr, kthreaddPid) ||
		(pidIs(attrs, pidAttr, kthreaddPid) && noParent(attrs))
}

// isLinux is false only when os.type says another system, or is not a string.
func isLinux(attrs pcommon.Map) bool {
	v, ok := attrs.Get(osTypeAttr)
	return !ok || (v.Type() == pcommon.ValueTypeStr && v.Str() == "linux")
}

// noParent is true when the parent pid is absent or 0.
func noParent(attrs pcommon.Map) bool {
	if _, ok := attrs.Get(parentPidAttr); !ok {
		return true
	}
	return pidIs(attrs, parentPidAttr, 0)
}

// pidIs reads an integer or a plain decimal string ("2", not "+2", "02" or " 2");
// any other type is not a match.
func pidIs(attrs pcommon.Map, key string, want int64) bool {
	v, ok := attrs.Get(key)
	if !ok {
		return false
	}
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return v.Int() == want
	case pcommon.ValueTypeStr:
		return v.Str() == strconv.FormatInt(want, 10)
	default:
		return false
	}
}
