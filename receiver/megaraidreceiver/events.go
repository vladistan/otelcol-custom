// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// errCursorCorrupt marks a saved position that is not a number.
var errCursorCorrupt = errors.New("the saved event position is not a number")

// kvStore is the part of a storage extension client the cursor needs.
type kvStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
}

// eventCursor keeps the last read event sequence number of each controller.
type eventCursor struct{ store kvStore }

func cursorKey(c controller) string { return "megaraid/" + c.id() + "/seq" }

// get returns the saved position. have is false when nothing is saved or the saved value is not
// a number (the position then restarts at the newest event); err is a storage failure, and the
// caller must not treat it as a first run.
func (e *eventCursor) get(ctx context.Context, c controller) (seq uint32, have bool, err error) {
	raw, err := e.store.Get(ctx, cursorKey(c))
	if err != nil {
		return 0, false, err
	}
	if len(raw) == 0 {
		return 0, false, nil
	}
	v, perr := strconv.ParseUint(string(raw), 10, 32)
	if perr != nil {
		return 0, false, errCursorCorrupt
	}
	return uint32(v), true, nil
}

func (e *eventCursor) set(ctx context.Context, c controller, seq uint32) error {
	return e.store.Set(ctx, cursorKey(c), []byte(strconv.FormatUint(uint64(seq), 10)))
}

// classLocale is the second mailbox word of EVENT_GET: the lowest class and every locale.
func classLocale(class int8) uint32 {
	return uint32(uint8(class))<<24 | evtLocaleAll //nolint:gosec // the class byte is signed on the wire
}

// readEvents reads the new events of one controller into the logs and queues the cursor update
// for after delivery. With no saved sequence number the first read starts from the newest event;
// a saved number above the newest one (log cleared) is reset to the newest.
func (s *scraper) readEvents(ctx context.Context, dev device, c controller, st *ctrlState, now time.Time, out *output) {
	st.evtFailed = false
	defer func() {
		if !st.evtFailed {
			st.evtEpisode = ""
		}
	}()
	raw, ok, err := s.cmd(dev, st, c, request{opcode: opEventGetInfo, dataLen: evtInfoLen})
	if err != nil || !ok {
		s.eventFailure(st, c, "state", err)
		return
	}
	state, err := decodeEventState(raw)
	if err != nil {
		s.warnOnce(st, "evt-short", "An event log state answer is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
		s.eventFailure(st, c, "state", err)
		return
	}
	saved, have, err := s.events.get(ctx, c)
	switch {
	case errors.Is(err, errCursorCorrupt):
		s.warnOnce(st, "evt-cursor", "The saved event position is not a number; the log restarts at the newest event", zap.String("controller", c.id()))
	case err != nil:
		// A failed read says nothing about the position, so the backlog is neither skipped nor replayed.
		s.eventFailure(st, c, "storage", err)
		return
	}
	if !have || saved > state.Newest {
		s.queueCursor(out, c, state.Newest)
		return
	}
	if state.Newest == saved {
		return
	}
	count := int(min(uint64(state.Newest-saved), uint64(s.cfg.Events.MaxPerScrape)))
	var m [mboxSize]byte
	binary.LittleEndian.PutUint32(m[0:], saved+1)
	binary.LittleEndian.PutUint32(m[4:], classLocale(s.minClass))
	raw, ok, err = s.cmd(dev, st, c, request{opcode: opEventGet, mbox: m, dataLen: evtHeader + evtSize*count})
	if err != nil || !ok {
		s.eventFailure(st, c, "read", err)
		return
	}
	events, err := decodeEvents(raw)
	if err != nil {
		s.warnOnce(st, "evt-list-short", "An event list answer is shorter than expected", zap.String("controller", c.id()), zap.Error(err))
		s.eventFailure(st, c, "read", err)
		return
	}
	st.evtEpisode = ""
	cursor := state.Newest
	if len(events) >= count && len(events) > 0 {
		cursor = events[len(events)-1].Sequence
	}
	rl := out.logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr(hwschema.AttrID, c.id())
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName(scopeName)
	for _, ev := range events {
		if ev.Class < s.minClass || ev.Sequence <= saved {
			continue
		}
		addEvent(sl, ev, now)
	}
	s.queueCursor(out, c, cursor)
}

// eventFailure counts a failed event read and logs it once for each run of failures of one kind.
// The metrics of the scrape are already sent, so without this line events could stop silently.
func (s *scraper) eventFailure(st *ctrlState, c controller, stage string, err error) {
	st.evtErrors++
	st.evtFailed = true
	if st.evtEpisode == stage {
		return
	}
	st.evtEpisode = stage
	fields := []zap.Field{zap.String("controller", c.id()), zap.String("stage", stage)}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	s.logger.Warn("Cannot read the controller event log; new events are not read until it works", fields...)
}

func (s *scraper) queueCursor(out *output, c controller, seq uint32) {
	out.commit = append(out.commit, func(ctx context.Context) error { return s.events.set(ctx, c, seq) })
}

func addEvent(sl plog.ScopeLogs, ev event, now time.Time) {
	r := sl.LogRecords().AppendEmpty()
	r.SetObservedTimestamp(pcommon.NewTimestampFromTime(now))
	r.SetTimestamp(pcommon.NewTimestampFromTime(now))
	r.Body().SetStr(ev.Text)
	r.SetSeverityText(className(ev.Class))
	r.SetSeverityNumber(severity(ev.Class))
	a := r.Attributes()
	a.PutInt("raid.event.sequence", int64(ev.Sequence))
	a.PutStr("raid.event.class", className(ev.Class))
	a.PutInt("raid.event.code", int64(ev.Code))
	a.PutInt("raid.event.locale", int64(ev.Locale))
}

func severity(c int8) plog.SeverityNumber {
	switch {
	case c <= classDebug:
		return plog.SeverityNumberDebug
	case c == classProgress:
		return plog.SeverityNumberDebug2
	case c == classInfo:
		return plog.SeverityNumberInfo
	case c == classWarning:
		return plog.SeverityNumberWarn
	case c == classCritical:
		return plog.SeverityNumberError
	}
	return plog.SeverityNumberFatal
}
