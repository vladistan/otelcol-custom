package collect

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

const (
	defaultInterval = 10 * time.Second
	maxGapIntervals = 3
)

// Option is a functional option for New; WithInterval is the only one.
type Option func(*Collector)

// WithInterval sets the configured collection interval. A gap between io
// samples of more than three intervals re-baselines the io integrator.
func WithInterval(interval time.Duration) Option {
	return func(c *Collector) { c.interval = interval }
}

// Collector reads every enumerated GPU through the port on each Collect.
type Collector struct {
	port     port.Port
	devices  []*deviceState
	toggles  port.Toggles
	start    time.Time
	logger   *zap.Logger
	interval time.Duration
}

var _ port.Collector = (*Collector)(nil)

// New builds a Collector. A toggle that is missing from toggles counts as
// enabled. start stamps the cumulative series; time comes from Collect.
func New(
	p port.Port,
	devices []port.DeviceIdentity,
	toggles port.Toggles,
	start time.Time,
	logger *zap.Logger,
	opts ...Option,
) *Collector {
	c := &Collector{
		port:     p,
		toggles:  toggles,
		start:    start,
		logger:   logger,
		interval: defaultInterval,
	}
	for _, id := range devices {
		c.devices = append(c.devices, newDeviceState(id))
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Collect never fails; it returns an empty batch when nothing was readable.
func (c *Collector) Collect(now time.Time) pmetric.Metrics {
	b := newBatch(now, c.start)
	for _, d := range c.devices {
		c.collectDevice(b, d, now)
	}
	if b.empty() {
		return pmetric.NewMetrics()
	}
	return b.md
}

func (c *Collector) on(metric string) bool {
	enabled, ok := c.toggles[metric]
	return !ok || enabled
}

// scrape is one device read within one Collect call.
type scrape struct {
	c         *Collector
	d         *deviceState
	b         *batch
	now       time.Time
	succeeded int
}

// query runs one port call unless the GPU is lost or the call is known
// unsupported. Failures are recorded; a success counts toward hw.status.
func query[T any](s *scrape, call string, read func() (T, error)) (T, bool) {
	var zero T
	if s.d.lost || s.d.unsupported[call] {
		return zero, false
	}
	value, err := read()
	if err != nil {
		s.d.recordFailure(s.c.logger, call, err)
		return zero, false
	}
	s.succeeded++
	return value, true
}

func (c *Collector) collectDevice(b *batch, d *deviceState, now time.Time) {
	if d.lost {
		c.emitStatus(b, d, false)
		return
	}
	s := &scrape{c: c, d: d, b: b, now: now}
	if dev, ok := c.deviceHandle(d); ok {
		c.readDevice(s, dev)
	}
	c.emitStatus(b, d, d.lost || s.succeeded > 0)
}

// deviceHandle caches the handle after the first success. A failed lookup
// is a failed call but does not count toward hw.status.
func (c *Collector) deviceHandle(d *deviceState) (port.Device, bool) {
	if d.handle != nil {
		return d.handle, true
	}
	dev, err := c.port.Device(d.id.Index)
	if err != nil {
		d.recordFailure(c.logger, "Device", err)
		return nil, false
	}
	d.handle = dev
	return dev, true
}

// emitStatus writes hw.status. known is false when no enabled call
// answered and the GPU is not lost, so all three states report 0.
func (c *Collector) emitStatus(b *batch, d *deviceState, known bool) {
	if !c.on(semconv.MetricStatus) {
		return
	}
	state := func(name string, value int64) {
		b.addInt(semconv.MetricStatus, d.id, value, attr{semconv.AttrState, name})
	}
	okValue, failedValue := int64(0), int64(0)
	switch {
	case d.lost:
		failedValue = 1
	case known:
		okValue = 1
	}
	state(semconv.StateOK, okValue)
	state(semconv.StateDegraded, 0)
	state(semconv.StateFailed, failedValue)
}
