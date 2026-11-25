// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/receiver/ippreceiver"

	metricState     = "printer.ipp.state"
	metricReason    = "printer.ipp.state_reason"
	metricMarker    = "printer.ipp.marker.level"
	metricReachable = "printer.ipp.reachable"
	metricAccepting = "printer.ipp.accepting_jobs"
	metricQueued    = "printer.ipp.queued_jobs"

	attrState    = "printer.ipp.state"
	attrReason   = "printer.ipp.reason"
	attrSeverity = "printer.ipp.reason.severity"
	attrMarkName = "printer.ipp.marker.name"
	attrMarkType = "printer.ipp.marker.type"

	reasonNone = "none"
)

var printerStates = map[int32]string{3: "idle", 4: "processing", 5: "stopped"}

// severities are the suffixes of a printer-state-reasons keyword.
var severities = []string{"report", "warning", "error"}

// series builds the gauges of one poll, each created with its first point.
type series struct {
	md      pmetric.Metrics
	sm      pmetric.ScopeMetrics
	printer string
	ts      pcommon.Timestamp
	gauges  map[string]pmetric.NumberDataPointSlice
}

func newSeries(printer string, now time.Time) *series {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	return &series{
		md:      md,
		sm:      sm,
		printer: printer,
		ts:      pcommon.NewTimestampFromTime(now),
		gauges:  map[string]pmetric.NumberDataPointSlice{},
	}
}

func (s *series) metrics() pmetric.Metrics { return s.md }

func (s *series) point(name, description, unit string) pmetric.NumberDataPoint {
	dps, ok := s.gauges[name]
	if !ok {
		m := s.sm.Metrics().AppendEmpty()
		m.SetName(name)
		m.SetDescription(description)
		m.SetUnit(unit)
		dps = m.SetEmptyGauge().DataPoints()
		s.gauges[name] = dps
	}
	dp := dps.AppendEmpty()
	dp.SetTimestamp(s.ts)
	dp.Attributes().PutStr("hw.type", "printer")
	dp.Attributes().PutStr("hw.id", s.printer)
	dp.Attributes().PutStr("hw.name", s.printer)
	return dp
}

// failedMetrics is the answer to a failed poll: reachable 0 and nothing else.
func failedMetrics(printer string, now time.Time) pmetric.Metrics {
	s := newSeries(printer, now)
	s.reachable(0)
	return s.metrics()
}

// printerMetrics turns the attributes of one answer into series; an attribute the
// printer left out gives no series, and reachable is 1.
func printerMetrics(printer string, attrs map[string][]value, now time.Time) pmetric.Metrics {
	s := newSeries(printer, now)
	s.reachable(1)
	s.state(attrs["printer-state"])
	s.reasons(attrs["printer-state-reasons"])
	s.accepting(attrs["printer-is-accepting-jobs"])
	s.queued(attrs["queued-job-count"])
	s.markers(attrs["marker-levels"], attrs["marker-names"], attrs["marker-types"])
	return s.metrics()
}

func (s *series) reachable(v int64) {
	dp := s.point(metricReachable, "1 when the last poll of the printer succeeded, 0 when it failed", "1")
	dp.SetIntValue(v)
}

func (s *series) state(vals []value) {
	if len(vals) == 0 {
		return
	}
	enum, ok := vals[0].asInt()
	if !ok {
		return
	}
	name, known := printerStates[enum]
	if !known {
		return
	}
	dp := s.point(metricState, "1 for the current printer-state", "1")
	dp.Attributes().PutStr(attrState, name)
	dp.SetIntValue(1)
}

func (s *series) reasons(vals []value) {
	for _, v := range vals {
		keyword := v.asString()
		if keyword == "" || keyword == reasonNone {
			continue
		}
		reason, severity := splitReason(keyword)
		dp := s.point(metricReason, "1 for each keyword of printer-state-reasons", "1")
		dp.Attributes().PutStr(attrReason, reason)
		dp.Attributes().PutStr(attrSeverity, severity)
		dp.SetIntValue(1)
	}
}

// splitReason cuts the -report, -warning or -error suffix; a keyword without one is an error.
func splitReason(keyword string) (reason, severity string) {
	for _, sev := range severities {
		if base, found := strings.CutSuffix(keyword, "-"+sev); found {
			return base, sev
		}
	}
	return keyword, "error"
}

func (s *series) accepting(vals []value) {
	if len(vals) == 0 {
		return
	}
	accepting, ok := vals[0].asBool()
	if !ok {
		return
	}
	dp := s.point(metricAccepting, "1 when the printer accepts jobs, 0 when it does not", "1")
	if accepting {
		dp.SetIntValue(1)
	} else {
		dp.SetIntValue(0)
	}
}

func (s *series) queued(vals []value) {
	if len(vals) == 0 {
		return
	}
	n, ok := vals[0].asInt()
	if !ok || n < 0 {
		return
	}
	dp := s.point(metricQueued, "Jobs queued on the printer", "{job}")
	dp.SetIntValue(int64(n))
}

// markers emits one ratio per level of 0 to 100; a negative level means unknown. A name or
// type is set only where the list is long enough.
func (s *series) markers(levels, names, types []value) {
	for i, lv := range levels {
		pct, ok := lv.asInt()
		if !ok || pct < 0 || pct > 100 {
			continue
		}
		dp := s.point(metricMarker, "Remaining level of a marker, a ratio from 0 to 1", "1")
		if i < len(names) {
			dp.Attributes().PutStr(attrMarkName, names[i].asString())
		}
		if i < len(types) {
			dp.Attributes().PutStr(attrMarkType, types[i].asString())
		}
		dp.SetDoubleValue(float64(pct) / 100)
	}
}
