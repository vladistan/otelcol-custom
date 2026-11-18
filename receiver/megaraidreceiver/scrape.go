// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// Command status values of the frame (MFI_STAT_*).
const (
	statusOK            = 0x00
	statusInvalidDCMD   = 0x02
	statusNotSupported  = 0x3d // seen on a 3108 for the battery capacity command
	statusDeviceMissing = 0x0c
	statusSCSIError     = 0x2d
)

// ctrlState is what the scraper remembers of one controller between scrapes.
type ctrlState struct {
	invalid         map[uint32]bool // commands the controller does not know; never sent again until restart
	notSupported    map[uint32]bool // commands reported as not supported; asked at every scrape, logged once per episode
	warned          map[string]bool
	episode         string
	smartLast       map[uint16]time.Time
	smartCache      map[uint16]*smartResult
	hung            <-chan struct{} // closed when a timed-out call returns to userspace
	eventErrorCount uint64          // failed event reads since start
	eventEpisode    string
	hasEventFailure bool
}

func newCtrlState() *ctrlState {
	return &ctrlState{
		invalid:      map[uint32]bool{},
		notSupported: map[uint32]bool{},
		warned:       map[string]bool{},
		smartLast:    map[uint16]time.Time{},
		smartCache:   map[uint16]*smartResult{},
	}
}

// scraper reads every megaraid_sas controller of the host once for each scrape. At most one scrape
// is in flight.
type scraper struct {
	logger *zap.Logger
	cfg    *Config
	find   func() ([]controller, error)
	open   func(hostNo int) (device, func(), error)
	now    func() time.Time
	start  time.Time
	events *eventCursor

	inFlight           atomic.Bool
	mu                 sync.Mutex
	state              map[string]*ctrlState
	hasLoggedNone      bool
	hasLoggedFindError bool
	minClass           int8
}

func newScraper(logger *zap.Logger, cfg *Config, find func() ([]controller, error), open func(int) (device, func(), error), now func() time.Time) *scraper {
	class, _ := parseClass(cfg.Events.MinClass)
	return &scraper{logger: logger, cfg: cfg, find: find, open: open, now: now, start: now(), state: map[string]*ctrlState{}, minClass: class}
}

// output is what one scrape produced. commit stores the event cursors; the receiver calls it after
// the logs reached the next consumer.
type output struct {
	metrics pmetric.Metrics
	logs    plog.Logs
	commit  []func(context.Context) error
}

func (s *scraper) stateFor(c controller) *ctrlState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[c.id()]
	if !ok {
		st = newCtrlState()
		s.state[c.id()] = st
	}
	return st
}

// warnOnce logs a warning the first time a key is seen for a controller.
func (s *scraper) warnOnce(st *ctrlState, key, msg string, fields ...zap.Field) {
	if st.warned[key] {
		return
	}
	st.warned[key] = true
	s.logger.Warn(msg, fields...)
}

func (s *scraper) scrape(ctx context.Context) output {
	out := output{metrics: pmetric.NewMetrics(), logs: plog.NewLogs()}
	if !s.inFlight.CompareAndSwap(false, true) {
		return out
	}
	defer s.inFlight.Store(false)

	ctrlList, err := s.find()
	if err != nil {
		if !s.hasLoggedFindError {
			s.hasLoggedFindError = true
			s.logger.Warn("Cannot list the megaraid_sas controllers", zap.Error(err))
		}
		return out
	}
	if len(ctrlList) == 0 {
		if !s.hasLoggedNone {
			s.hasLoggedNone = true
			s.logger.Info("No megaraid_sas controller found; nothing is sent to any device")
		}
		return out
	}
	now := s.now()
	for _, c := range ctrlList {
		if ctx.Err() != nil {
			break
		}
		s.scrapeOne(ctx, c, now, &out)
	}
	return out
}

// errClass groups the errors that skip a scrape, so one episode logs one line.
func errClass(err error) string {
	switch {
	case errors.Is(err, errNeedsCapability):
		return "capability"
	case errors.Is(err, errNodeAccess):
		return "access"
	case errors.Is(err, errStillInFlight):
		return "in-flight"
	case errors.Is(err, errNodeMissing):
		return "node"
	case errors.Is(err, errControllerGone):
		return "gone"
	case errors.Is(err, errControllerBusy):
		return "busy"
	case errors.Is(err, errTimeout), errors.Is(err, errDeviceBroken):
		return "timeout"
	case errors.Is(err, errInterrupted):
		return "interrupted"
	case errors.Is(err, errRejected):
		return "rejected"
	}
	return "error: " + err.Error()
}

func (s *scraper) scrapeOne(ctx context.Context, c controller, now time.Time, out *output) {
	st := s.stateFor(c)
	b := newBuilder(out.metrics, pcommon.NewTimestampFromTime(s.start), pcommon.NewTimestampFromTime(now))

	fail := func(err error) {
		class := errClass(err)
		if st.episode == class {
			return
		}
		st.episode = class
		s.logger.Warn("Skipping the scrape of a MegaRAID controller", zap.String("controller", c.id()), zap.Error(err))
		if errors.Is(err, errControllerGone) {
			b.gauge(metricReachable, ident{id: c.id()}, typeController, 0)
		}
	}

	if st.hung != nil {
		select {
		case <-st.hung:
			st.hung = nil
		default:
			fail(errStillInFlight)
			return
		}
	}
	dev, closeFn, err := s.open(c.HostNo)
	if err != nil {
		fail(err)
		return
	}
	defer func() {
		if h, ok := dev.(hangReporter); ok {
			if ch := h.hung(); ch != nil {
				st.hung = ch
				go func() {
					<-ch
					closeFn()
				}()
				return
			}
		}
		closeFn()
	}()
	snap, err := s.read(dev, c, st, now)
	if err != nil {
		fail(err)
		return
	}
	st.episode = ""
	b.gauge(metricReachable, ident{id: c.id()}, typeController, 1)
	snap.emit(b, func(msg string) { s.warnOnce(st, msg, msg, zap.String("controller", c.id())) })

	if s.cfg.Events.Enabled && s.events != nil {
		s.readEvents(ctx, dev, c, st, now, out)
		b.count(metricEventErrors, ident{id: c.id()}, typeController, st.eventErrorCount)
	}
}

// execRead sends one allowed DCMD. ok is false when the command is skipped or the controller answers
// with a status other than success; an error ends the read of the controller.
func (s *scraper) execRead(dev device, st *ctrlState, c controller, req request) ([]byte, bool, error) {
	if st.invalid[req.opcode] {
		return nil, false, nil
	}
	resp, err := dev.exec(req)
	if err != nil {
		return nil, false, err
	}
	switch resp.status {
	case statusOK:
		delete(st.notSupported, req.opcode)
		return resp.data, true, nil
	case statusInvalidDCMD:
		st.invalid[req.opcode] = true
		s.logger.Info("The controller does not know a command and it is not sent again until the receiver restarts",
			zap.String("controller", c.id()), zap.String("command", allowed[req.opcode]))
	case statusNotSupported:
		if !st.notSupported[req.opcode] {
			st.notSupported[req.opcode] = true
			s.logger.Info("The controller reports a command as not supported and it is asked again at every scrape",
				zap.String("controller", c.id()), zap.String("command", allowed[req.opcode]))
		}
	default:
		s.warnOnce(st, fmt.Sprintf("%#x/%d", req.opcode, resp.status), "The controller refused a read command",
			zap.String("controller", c.id()), zap.String("command", allowed[req.opcode]), zap.Uint8("status", resp.status))
	}
	return nil, false, nil
}

func mailbox(b ...byte) [mboxSize]byte {
	var m [mboxSize]byte
	copy(m[:], b)
	return m
}

func (s *scraper) read(dev device, c controller, st *ctrlState, now time.Time) (*snapshot, error) {
	raw, ok, err := s.execRead(dev, st, c, request{opcode: opCtrlGetInfo, dataLen: ctrlInfoRequestLen})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("the controller did not answer the controller info command")
	}
	info, err := decodeCtrlInfo(raw)
	if err != nil {
		return nil, err
	}
	if !st.warned["product"] {
		st.warned["product"] = true
		if !info.supported() {
			s.logger.Info("This MegaRAID product is outside the supported list and is read best-effort",
				zap.String("controller", c.id()), zap.String("product", info.Product))
		}
	}
	snap := &snapshot{ctrl: c, info: info}

	targets, err := s.listLDs(dev, st, c, info)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		l, ok, err := s.readLD(dev, st, c, t)
		if err != nil {
			return nil, err
		}
		if ok {
			snap.lds = append(snap.lds, l)
		}
	}

	addrs, err := s.listPDs(dev, st, c, info)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.SCSIType != 0 {
			continue // enclosure services and other devices that are not drives
		}
		raw, ok, err := s.execRead(dev, st, c, request{opcode: opPDGetInfo, mbox: mailbox(byte(a.DeviceID), byte(a.DeviceID>>8)), dataLen: pdInfoReqLen})
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		p, err := decodePDInfo(raw)
		if err != nil {
			s.warnOnce(st, "pd-short", "A drive info answer is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
			continue
		}
		p.DeviceID = a.DeviceID
		snap.pds = append(snap.pds, pdRecord{addr: a, info: p})
	}

	if info.HasBBU {
		if snap.bbu, err = s.readBBU(dev, st, c); err != nil {
			return nil, err
		}
	}
	if s.cfg.Smart {
		s.readSmart(dev, c, st, snap, now)
	}
	return snap, nil
}

func (s *scraper) listLDs(dev device, st *ctrlState, c controller, info ctrlInfo) ([]uint8, error) {
	order := []uint32{opLDGetList, opLDListQuery}
	if info.extendedLDs() {
		order = []uint32{opLDListQuery, opLDGetList}
	}
	for _, op := range order {
		req := request{opcode: op, dataLen: ldListReqLen}
		if op == opLDListQuery {
			req = request{opcode: op, mbox: mailbox(ldQueryAll), dataLen: ldQueryReqLen}
		}
		raw, ok, err := s.execRead(dev, st, c, req)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		decode := decodeLDList
		if op == opLDListQuery {
			decode = decodeLDQuery
		}
		ids, err := decode(raw)
		if err != nil {
			s.warnOnce(st, "ld-short", "A logical drive list is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
			continue
		}
		return ids, nil
	}
	return nil, nil
}

func (s *scraper) listPDs(dev device, st *ctrlState, c controller, info ctrlInfo) ([]pdAddr, error) {
	order := []uint32{opPDGetList, opPDListQuery}
	if info.extendedPDs() {
		order = []uint32{opPDListQuery, opPDGetList}
	}
	for _, op := range order {
		req := request{opcode: op, dataLen: pdListReqLen}
		if op == opPDListQuery {
			req.mbox = mailbox(pdQueryExposed)
		}
		raw, ok, err := s.execRead(dev, st, c, req)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		addrs, err := decodePDList(raw)
		if err != nil {
			s.warnOnce(st, "pd-list-short", "A drive list is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
			continue
		}
		return addrs, nil
	}
	return nil, nil
}

func (s *scraper) readLD(dev device, st *ctrlState, c controller, target uint8) (ldInfo, bool, error) {
	raw, ok, err := s.execRead(dev, st, c, request{opcode: opLDGetInfo, mbox: mailbox(target), dataLen: ldInfoReqLen})
	if err != nil || !ok {
		return ldInfo{}, false, err
	}
	l, err := decodeLDInfo(raw)
	if err != nil {
		s.warnOnce(st, "ld-info-short", "A logical drive info answer is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
		return ldInfo{}, false, nil
	}
	l.Target = target
	raw, ok, err = s.execRead(dev, st, c, request{opcode: opLDGetProp, mbox: mailbox(target), dataLen: ldPropReqLen})
	if err != nil {
		return ldInfo{}, false, err
	}
	if ok {
		name, cache, perr := decodeLDProps(raw)
		if perr != nil {
			s.warnOnce(st, "ld-prop-short", "A logical drive properties answer is shorter than expected", zap.String("controller", c.id()), zap.Error(perr))
			return l, true, nil
		}
		if name != "" {
			l.Name = name
		}
		l.Cache = cache
	}
	return l, true, nil
}

func (s *scraper) readBBU(dev device, st *ctrlState, c controller) (*bbuInfo, error) {
	status, ok, err := s.execRead(dev, st, c, request{opcode: opBBUGetStatus, dataLen: bbuStatusLen})
	if err != nil || !ok {
		return nil, err
	}
	capacity, _, err := s.execRead(dev, st, c, request{opcode: opBBUGetCapacityInfo, dataLen: bbuCapacityLen})
	if err != nil {
		return nil, err
	}
	props, _, err := s.execRead(dev, st, c, request{opcode: opBBUGetProp, dataLen: bbuPropLen})
	if err != nil {
		return nil, err
	}
	bb, err := decodeBBU(status, capacity, props)
	if err != nil {
		s.warnOnce(st, "bbu-short", "A battery status answer is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
		return nil, nil
	}
	return &bb, nil
}

// compile-time check that the SMART decoders are the ones the transport serves.
var _ smartdecode.ATATransport = passTransport{}
