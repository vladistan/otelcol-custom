package collect

import "time"

// ioIntegrator accumulates bytes from sampled KB/s rates over measured time.
// The total is float64 and emitted truncated (D-21).
type ioIntegrator struct {
	started bool
	last    time.Time
	total   float64
}

// sample folds in one successful rate sample and returns the total to emit.
// The first sample emits 0. A gap longer than maxGap adds nothing and becomes
// the new baseline (D-25).
func (i *ioIntegrator) sample(now time.Time, kilobytesPerSecond uint32, maxGap time.Duration) uint64 {
	if !i.started {
		i.started = true
		i.last = now
		return 0
	}
	gap := now.Sub(i.last)
	if gap > 0 && gap <= maxGap {
		i.total += float64(kilobytesPerSecond) * 1024 * gap.Seconds()
	}
	i.last = now
	return uint64(i.total)
}

// eccIntegrator turns a raw VOLATILE ECC counter into a monotonic total that
// starts at 0 at the first sample (D-15).
type eccIntegrator struct {
	started  bool
	baseline uint64
	previous uint64
	total    uint64
}

// sample returns the total to emit for one raw reading. A reading below the
// previous one means the driver reloaded: the errors seen since the baseline
// and the new raw value are both kept.
func (e *eccIntegrator) sample(raw uint64) uint64 {
	if !e.started {
		e.started = true
		e.baseline = raw
		e.previous = raw
		return 0
	}
	if raw < e.previous {
		e.total += (e.previous - e.baseline) + raw
		e.baseline = raw
	}
	e.previous = raw
	return e.total + (raw - e.baseline)
}
