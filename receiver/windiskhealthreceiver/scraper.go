// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

type scraper struct {
	logger     *zap.Logger
	bus        bus
	sel        selector
	maxDevices int
	timeout    time.Duration
	skipped    metric.Int64Counter
	start      pcommon.Timestamp

	// active holds, for each disk, the reasons already logged while they last; a reason that does not occur
	// in an interval leaves the set, and logs again when it returns.
	active map[string]map[string]bool
	// known holds the identity of each disk from its last full SMART read, so a failed read keeps the series.
	known     map[string]ident
	capWarned bool
}

func newScraper(logger *zap.Logger, cfg *Config, b bus, skipped metric.Int64Counter, start time.Time) (*scraper, error) {
	sel, err := newSelector(cfg.Include, cfg.Exclude)
	if err != nil {
		return nil, err
	}
	return &scraper{
		logger:     logger,
		bus:        b,
		sel:        sel,
		maxDevices: cfg.MaxDevices,
		timeout:    cfg.CommandTimeout,
		skipped:    skipped,
		start:      pcommon.NewTimestampFromTime(start),
		active:     map[string]map[string]bool{},
		known:      map[string]ident{},
	}, nil
}

// scrape reads the selected disks one at a time. A disk that fails is skipped for this interval.
func (s *scraper) scrape(ctx context.Context, now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	names, err := s.bus.discover()
	if err != nil {
		s.logger.Warn("Failed to list the disks", zap.Error(err))
		return md
	}
	names = s.sel.filter(names)
	if len(names) > s.maxDevices {
		if !s.capWarned {
			s.capWarned = true
			s.logger.Warn("More disks than max_devices; the rest are not read",
				zap.Int("devices", len(names)), zap.Int("max_devices", s.maxDevices))
		}
		names = names[:s.maxDevices]
	} else {
		s.capWarned = false
	}
	b := newBuilder(md, s.start, pcommon.NewTimestampFromTime(now))
	for _, name := range names {
		if ctx.Err() != nil {
			break // shutdown: finish the disk in hand and read no more
		}
		s.read(ctx, b, name)
	}
	return md
}

// reading is what the steps of one disk gave.
type reading struct {
	desc          descriptor
	celsius       int
	hasTemp       bool
	isFailing     bool
	hasPrediction bool
	ata           *smartdecode.ATAData
	nvme          *smartdecode.NVMeData
}

// diskRun holds the reasons that occurred for one disk in one interval.
type diskRun struct {
	name string
	seen map[string]bool
}

// fail counts one failed step and reports whether the rest of the disk must wait for the next interval: a
// timeout or a refusal of the privilege ends the disk, any other failure leaves the later steps.
func (s *scraper) fail(ctx context.Context, run *diskRun, err error) (stop bool) {
	s.skip(ctx, run, err)
	return errors.Is(err, smartdecode.ErrTimeout) || errors.Is(err, smartdecode.ErrPermission)
}

func (s *scraper) read(ctx context.Context, b *builder, name string) {
	run := &diskRun{name: name, seen: map[string]bool{}}
	defer func() {
		if len(run.seen) == 0 {
			delete(s.active, name)
			return
		}
		s.active[name] = run.seen
	}()

	disk, err := s.bus.open(name, s.timeout)
	if err != nil {
		s.skip(ctx, run, err)
		return
	}
	defer func() { _ = disk.Close() }()

	var r reading
	if r.desc, err = disk.descriptor(); err != nil {
		s.skip(ctx, run, err)
		return
	}
	kind := r.desc.kind()
	if kind == kindSkip {
		s.skip(ctx, run, smartdecode.ErrUnsupported)
		return
	}

	// A timeout or a refusal ends the steps, and what was read before it is still sent.
	if r.celsius, err = disk.temperature(); err != nil {
		if s.fail(ctx, run, err) {
			s.emit(b, name, r)
			return
		}
	} else {
		r.hasTemp = true
	}
	if r.isFailing, err = disk.predictFailure(); err != nil {
		if s.fail(ctx, run, err) {
			s.emit(b, name, r)
			return
		}
	} else {
		r.hasPrediction = true
	}
	if kind == kindNVMe {
		var data smartdecode.NVMeData
		if data, err = smartdecode.ReadNVMe(disk); err == nil {
			r.nvme = &data
		}
	} else {
		var data smartdecode.ATAData
		if data, err = smartdecode.ReadATA(disk, smartdecode.ATAOptions{}); err == nil {
			r.ata = &data
		}
	}
	if err != nil {
		s.fail(ctx, run, err)
	}
	s.emit(b, name, r)
}

func (s *scraper) emit(b *builder, name string, r reading) {
	id, ok := s.known[name]
	if !ok {
		id = ident{id: name, name: name, model: strings.TrimSpace(r.desc.vendor + " " + r.desc.product), serial: r.desc.serial, firmware: r.desc.firmware}
		if r.desc.kind() == kindNVMe {
			id.diskType = hwschema.DiskNVMe
		}
	}
	state := ""
	switch {
	case r.nvme != nil:
		id = ident{
			id: firstNonEmpty(r.nvme.Identify.Serial, name), name: name, model: r.nvme.Identify.Model,
			serial: r.nvme.Identify.Serial, firmware: r.nvme.Identify.Firmware, diskType: hwschema.DiskNVMe,
		}
		s.known[name] = id
		state = smartdecode.NVMeState(r.nvme.Smart.CriticalWarning)
		b.emitNVMe(id, *r.nvme, !r.hasTemp)
	case r.ata != nil:
		info := r.ata.Identify
		id = ident{
			id: firstNonEmpty(info.Serial, info.WWN, name), name: name, model: info.Model,
			serial: info.Serial, firmware: info.Firmware, diskType: hwschema.DiskHDD,
		}
		if info.IsSSD() {
			id.diskType = hwschema.DiskSSD
		}
		s.known[name] = id
		switch r.ata.Health {
		case smartdecode.HealthOK:
			state = hwschema.StateOK
		case smartdecode.HealthPredictedFailure:
			state = hwschema.StatePredictedFailure
		}
		b.emitATA(id, *r.ata, !r.hasTemp)
	}
	if r.hasPrediction {
		pred := hwschema.StateOK
		if r.isFailing {
			pred = hwschema.StatePredictedFailure
		}
		state = worse(firstNonEmpty(state, pred), pred)
	}
	if state != "" {
		b.status(id, state)
	}
	if r.hasTemp {
		b.temperature(id, float64(r.celsius))
	}
}

// skip counts a skipped step by reason and logs the reason once while it lasts for the disk.
func (s *scraper) skip(ctx context.Context, run *diskRun, err error) {
	reason := reasonError
	switch {
	case errors.Is(err, smartdecode.ErrPermission):
		reason = reasonPermission
	case errors.Is(err, smartdecode.ErrTimeout):
		reason = reasonTimeout
	case errors.Is(err, smartdecode.ErrUnsupported), errors.Is(err, smartdecode.ErrSMARTUnsupported), errors.Is(err, smartdecode.ErrSMARTDisabled):
		reason = reasonUnsupported
	}
	s.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String(reasonLabelKey, reason)))

	already := run.seen[reason] || s.active[run.name][reason]
	run.seen[reason] = true
	if already {
		return
	}
	name := run.name
	switch reason {
	case reasonPermission:
		s.logger.Error("The disk refused the request; the collector must run as LocalSystem or with the privilege to open disks",
			zap.String("device", name), zap.String("missing_privilege", "administrator access to the disk"), zap.Error(err))
	case reasonUnsupported:
		s.logger.Info("Skipped a step; the disk or its driver does not answer it", zap.String("device", name), zap.String("reason", reason), zap.Error(err))
	default:
		s.logger.Warn("Skipped a step for this interval", zap.String("device", name), zap.String("reason", reason), zap.Error(err))
	}
}
