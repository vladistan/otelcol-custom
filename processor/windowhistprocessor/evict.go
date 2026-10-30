// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import "context"

// evictAfter is how many consecutive windows without a sample remove a series.
const evictAfter = 2

// markIdle records that a series had no samples this window and reports
// whether it should be removed. Its baseline and history go with it.
func (s *series) markIdle() bool {
	s.idleWindows++
	return s.idleWindows >= evictAfter
}

func (p *windowProcessor) activeSeries() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return int64(len(p.series))
}

// flushAll closes every open window at the current time, so the partial
// window of a graceful shutdown keeps its real start and end.
func (p *windowProcessor) flushAll(ctx context.Context) error {
	var first error
	for _, send := range p.sendIntervals() {
		if err := p.flush(ctx, send, p.now()); err != nil && first == nil {
			first = err
		}
	}
	return first
}
