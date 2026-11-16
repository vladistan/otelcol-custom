// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"slices"
	"time"
)

// window is a half-open span of CloudWatch time, aligned to UTC.
type window struct{ start, end time.Time }

// windowEnd is the latest complete period boundary at or before now minus the delay.
func windowEnd(now time.Time, delay, period time.Duration) time.Time {
	t := now.Add(-delay).UTC()
	seconds := int64(period / time.Second)
	if seconds <= 0 {
		return t
	}
	return time.Unix(t.Unix()/seconds*seconds, 0).UTC()
}

// missedWindows lists the windows of the given length that end after last, up to
// end, oldest first. Windows that start before end minus maxCatchup are dropped,
// but the newest is always kept. Without a saved end only the newest is read.
func missedWindows(end time.Time, interval time.Duration, last time.Time, haveLast bool, maxCatchup time.Duration) []window {
	if haveLast && !last.Before(end) {
		return nil
	}
	newest := window{start: end.Add(-interval), end: end}
	if !haveLast {
		return []window{newest}
	}
	cutoff := end.Add(-maxCatchup)
	out := []window{newest}
	for e := end.Add(-interval); e.After(last); e = e.Add(-interval) {
		w := window{start: e.Add(-interval), end: e}
		if w.start.Before(cutoff) {
			break
		}
		out = append(out, w)
	}
	slices.Reverse(out)
	return out
}
