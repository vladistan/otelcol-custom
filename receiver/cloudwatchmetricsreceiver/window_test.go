// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetricsreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func utc(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, time.UTC)
}

func TestWindowEndAlignsTheDelayedTimeDownToThePeriod(t *testing.T) {
	tests := []struct {
		name          string
		now           time.Time
		delay, period time.Duration
		want          time.Time
	}{
		{"daily at 02:00 with a one hour delay", utc(15, 2, 0), time.Hour, 24 * time.Hour, utc(15, 0, 0)},
		{"daily at 00:30 with a one hour delay reaches back a day", utc(15, 0, 30), time.Hour, 24 * time.Hour, utc(14, 0, 0)},
		{"quarter hour", utc(15, 14, 7), 10 * time.Minute, 15 * time.Minute, utc(15, 13, 45)},
		{"exactly on a boundary", utc(15, 14, 15), 0, 15 * time.Minute, utc(15, 14, 15)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.want.Equal(windowEnd(tt.now, tt.delay, tt.period)), "got %v", windowEnd(tt.now, tt.delay, tt.period))
		})
	}
}

func ends(ws []window) []time.Time {
	out := make([]time.Time, len(ws))
	for i, w := range ws {
		out[i] = w.end
	}
	return out
}

func TestMissedWindowsWithoutStateIsThePreviousCompleteWindowOnly(t *testing.T) {
	got := missedWindows(utc(15, 0, 0), 24*time.Hour, time.Time{}, false, 72*time.Hour)

	assert.Equal(t, []window{{start: utc(14, 0, 0), end: utc(15, 0, 0)}}, got)
}

func TestMissedWindowsInsideTheSameWindowIsEmpty(t *testing.T) {
	got := missedWindows(utc(15, 0, 0), 24*time.Hour, utc(15, 0, 0), true, 72*time.Hour)

	assert.Empty(t, got)
}

func TestMissedWindowsAfterATwoDayStopReadsBothDaysOldestFirst(t *testing.T) {
	got := missedWindows(utc(17, 0, 0), 24*time.Hour, utc(15, 0, 0), true, 72*time.Hour)

	assert.Equal(t, []time.Time{utc(16, 0, 0), utc(17, 0, 0)}, ends(got))
	assert.True(t, utc(15, 0, 0).Equal(got[0].start))
}

func TestMissedWindowsAfterAFiveDayStopReadsOnlyTheLastThree(t *testing.T) {
	got := missedWindows(utc(20, 0, 0), 24*time.Hour, utc(15, 0, 0), true, 72*time.Hour)

	assert.Equal(t, []time.Time{utc(18, 0, 0), utc(19, 0, 0), utc(20, 0, 0)}, ends(got))
}

func TestMissedWindowsOfAShortTierTileWithoutGapsOrOverlap(t *testing.T) {
	got := missedWindows(utc(15, 14, 0), 15*time.Minute, utc(15, 13, 15), true, 72*time.Hour)

	assert.Equal(t, []time.Time{utc(15, 13, 30), utc(15, 13, 45), utc(15, 14, 0)}, ends(got))
	for i := 1; i < len(got); i++ {
		assert.True(t, got[i-1].end.Equal(got[i].start), "window %d starts where the one before ends", i)
	}
}

func TestMissedWindowsStateAheadOfNowIsEmpty(t *testing.T) {
	assert.Empty(t, missedWindows(utc(15, 0, 0), 24*time.Hour, utc(16, 0, 0), true, 72*time.Hour))
}

func TestMissedWindowsCatchupBelowOneIntervalStillReadsTheLatest(t *testing.T) {
	got := missedWindows(utc(17, 0, 0), 24*time.Hour, utc(15, 0, 0), true, time.Hour)

	assert.Equal(t, []time.Time{utc(17, 0, 0)}, ends(got), "the cap never removes the newest window")
}
