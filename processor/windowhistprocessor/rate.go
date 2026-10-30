// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

// addCounter turns a cumulative counter reading into a rate sample: the
// difference to the previous reading over the measured time between them.
func (p *windowProcessor) addCounter(s *series, dp pmetric.NumberDataPoint, value float64, at time.Time) {
	if s.route.KeepCounter {
		dp.CopyTo(s.lastCounter)
		s.hasCounter = true
	}
	next := baseline{valid: true, value: value, at: at, start: dp.StartTimestamp()}
	prev := s.base
	switch {
	case !prev.valid:
		s.base = next
	case prev.start != next.start || value < prev.value:
		p.tel.discard(reasonCounterReset)
		s.base = next
	case !at.After(prev.at):
		p.tel.discard(reasonInvalid)
	default:
		s.window.Update((value - prev.value) / at.Sub(prev.at).Seconds())
		s.base = next
	}
}

// checkSpacing warns once per group when samples arrive further from the
// declared scrape interval than half of it.
func (p *windowProcessor) checkSpacing(s *series, at time.Time) {
	prevAt, hadPrev := s.prevAt, s.hasPrev
	s.prevAt, s.hasPrev = at, true
	if !hadPrev || p.spacingWarned[s.route.name] {
		return
	}
	scrape := s.route.ScrapeInterval
	gap := at.Sub(prevAt)
	diff := gap - scrape
	if diff < 0 {
		diff = -diff
	}
	if diff*2 > scrape {
		p.spacingWarned[s.route.name] = true
		p.warn(fmt.Sprintf("windowhist: group %q samples arrive %v apart; declared scrape_interval is %v",
			s.route.name, gap, scrape))
	}
}
