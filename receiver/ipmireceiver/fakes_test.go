// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

// fakeClock is the time of a test; the fake BMC advances it to model latency.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeBMC is a board that answers the session calls from fixtures and counts them.
type fakeBMC struct {
	mu    sync.Mutex
	clock *fakeClock

	device   deviceInfo
	stamp    time.Time
	records  []sdrRecord
	readings map[uint8]sensorReading
	absent   map[uint8]bool // sensors that answer "not present"

	power       float64
	powerErrs   []error // answers of the next power reads, one each; then power
	dialErr     error   // returned for every dial at any level
	adminErr    error   // returned for dials at administrator level only
	hang        bool    // every call blocks until the context ends
	dialLatency time.Duration

	frus         map[uint8][]byte // FRU images by device; a device with none answers "not present"
	fruErrs      map[uint8]error
	fruDevices   []fruDevice
	fruListErr   error
	fruReadCount int
	fruListCount int
	fruLatency   time.Duration // each FRU read takes this long, on the fake clock and in real time
	fruHook      func()        // runs in every FRU read

	fruIDs []uint8 // devices asked, in order

	closeCtxErrs []error // ctx.Err() of every Close
	powerCtxErrs []error // ctx.Err() of every power read

	sdrReads       int
	sensorReads    []uint8
	powerReads     int
	dials          []string // privilege level of every dial
	opened, closed int
	calls          []string // names of every call, in order
}

func newBoard(clock *fakeClock, records ...sdrRecord) *fakeBMC {
	b := &fakeBMC{clock: clock, device: deviceInfo{firmware: "3.26"}, stamp: clock.Now(), records: records,
		readings: map[uint8]sensorReading{}, absent: map[uint8]bool{}, power: 194}
	return b
}

func (b *fakeBMC) dial(ctx context.Context, _ BMCConfig, level string) (bmcSession, error) {
	b.mu.Lock()
	b.dials = append(b.dials, level)
	err := b.dialErr
	if err == nil && level == "administrator" {
		err = b.adminErr
	}
	latency := b.dialLatency
	hang := b.hang
	b.mu.Unlock()
	if hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if latency > 0 {
		b.clock.Advance(latency)
	}
	b.mu.Lock()
	b.opened++
	b.mu.Unlock()
	return &fakeSession{b: b}, nil
}

type fakeSession struct{ b *fakeBMC }

func (s *fakeSession) record(name string) {
	s.b.mu.Lock()
	s.b.calls = append(s.b.calls, name)
	s.b.mu.Unlock()
}

func (s *fakeSession) GetDeviceID(context.Context) (deviceInfo, error) {
	s.record("GetDeviceID")
	return s.b.device, nil
}

func (s *fakeSession) GetSDRsMap(_ context.Context, known time.Time) (sdrSet, bool, error) {
	s.record("GetSDRsMap")
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if !known.IsZero() && known.Equal(s.b.stamp) {
		return sdrSet{}, true, nil
	}
	s.b.sdrReads++
	return sdrSet{stamp: s.b.stamp, records: s.b.records}, false, nil
}

func (s *fakeSession) GetSensorReading(_ context.Context, rec sdrRecord) (sensorReading, error) {
	s.record("GetSensorReading")
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.sensorReads = append(s.b.sensorReads, rec.number)
	if s.b.absent[rec.number] {
		return sensorReading{}, errNotPresent
	}
	return s.b.readings[rec.number], nil
}

func (s *fakeSession) GetDCMIPowerReading(ctx context.Context) (float64, error) {
	s.record("GetDCMIPowerReading")
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.powerReads++
	s.b.powerCtxErrs = append(s.b.powerCtxErrs, ctx.Err())
	if len(s.b.powerErrs) > 0 {
		err := s.b.powerErrs[0]
		s.b.powerErrs = s.b.powerErrs[1:]
		return 0, err
	}
	return s.b.power, nil
}

func (s *fakeSession) ListFRUDevices(context.Context) ([]fruDevice, error) {
	s.record("ListFRUDevices")
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.fruListCount++
	if s.b.fruListErr != nil {
		return nil, s.b.fruListErr
	}
	return s.b.fruDevices, nil
}

func (s *fakeSession) ReadFRU(_ context.Context, id uint8) ([]byte, error) {
	s.record("ReadFRU")
	if s.b.fruHook != nil {
		s.b.fruHook()
	}
	if s.b.fruLatency > 0 {
		s.b.clock.Advance(s.b.fruLatency)
		time.Sleep(s.b.fruLatency)
	}
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.fruReadCount++
	s.b.fruIDs = append(s.b.fruIDs, id)
	if err := s.b.fruErrs[id]; err != nil {
		return nil, err
	}
	img, ok := s.b.frus[id]
	if !ok {
		return nil, errNotPresent
	}
	return img, nil
}

func (s *fakeSession) Close(ctx context.Context) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.closed++
	s.b.closeCtxErrs = append(s.b.closeCtxErrs, ctx.Err())
	return nil
}

func (b *fakeBMC) fruReadIDs() []uint8 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]uint8(nil), b.fruIDs...)
}

// Fixture builders.

func ptrTo(v float64) *float64 { return &v }

// labBoard builds a board with the given number of temperature, fan and voltage
// sensors, each reading a fixed value, like the X9 boards of the lab.
func labBoard(clock *fakeClock, temps, fans, volts int) *fakeBMC {
	var recs []sdrRecord
	b := newBoard(clock)
	n := uint8(1)
	add := func(kind sensorKind, name string, value float64) {
		recs = append(recs, sdrRecord{number: n, name: name, kind: kind})
		b.readings[n] = sensorReading{value: value}
		n++
	}
	for i := 0; i < temps; i++ {
		add(kindTemperature, fmt.Sprintf("Temp %d", i), 30+float64(i))
	}
	for i := 0; i < fans; i++ {
		add(kindFan, fmt.Sprintf("FAN %d", i), 4000+float64(i))
	}
	for i := 0; i < volts; i++ {
		add(kindVoltage, fmt.Sprintf("V %d", i), 1.2)
	}
	b.records = recs
	return b
}

// Result helpers.

func pointCount(md pmetric.Metrics, name string) int {
	if md.ResourceMetrics().Len() == 0 {
		return 0
	}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i).Gauge().DataPoints().Len()
		}
	}
	return 0
}

func metricNamesOf(md pmetric.Metrics) []string {
	var out []string
	if md.ResourceMetrics().Len() == 0 {
		return out
	}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		out = append(out, ms.At(i).Name())
	}
	return out
}

func dataPoints(md pmetric.Metrics, name string) pmetric.NumberDataPointSlice {
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < ms.Len(); i++ {
		if ms.At(i).Name() == name {
			return ms.At(i).Gauge().DataPoints()
		}
	}
	return pmetric.NewNumberDataPointSlice()
}
