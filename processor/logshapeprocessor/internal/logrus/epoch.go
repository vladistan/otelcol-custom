// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrus

import "time"

var (
	epochMin = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	epochMax = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// EpochTime reads a number as seconds, milliseconds, microseconds or nanoseconds since 1970, by its size,
// and accepts it only when the time falls between the years 2000 and 2100.
func EpochTime(v float64) (time.Time, bool) {
	var secs float64
	switch {
	case v <= 0:
		return time.Time{}, false
	case v < 1e11:
		secs = v
	case v < 1e14:
		secs = v / 1e3
	case v < 1e17:
		secs = v / 1e6
	case v < 1e20:
		secs = v / 1e9
	default:
		return time.Time{}, false
	}
	whole := int64(secs)
	t := time.Unix(whole, int64((secs-float64(whole))*1e9)).UTC()
	if t.Before(epochMin) || !t.Before(epochMax) {
		return time.Time{}, false
	}
	return t, true
}
