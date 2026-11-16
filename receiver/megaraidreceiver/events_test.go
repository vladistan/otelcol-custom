// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
)

// memStore is the file_storage client the tests use.
type memStore struct {
	mu      sync.Mutex
	data    map[string][]byte
	failSet error
	failGet error
}

func newMemStore() *memStore { return &memStore{data: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet != nil {
		return nil, m.failGet
	}
	return m.data[key], nil
}

func (m *memStore) Set(_ context.Context, key string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSet != nil {
		return m.failSet
	}
	m.data[key] = v
	return nil
}

func (m *memStore) seq(c controller) uint32 {
	v, _ := strconv.ParseUint(string(m.data[cursorKey(c)]), 10, 32)
	return uint32(v)
}

// eventLog serves EVENT_GET_INFO and EVENT_GET for sequence numbers 1..newest, with a class for
// each sequence number given by classOf.
func eventLog(dev *fakeDevice, newest uint32, classOf func(uint32) int8) {
	dev.set(opEventGetInfo, evtStateBuf(newest, 1))
	dev.dynamic[opEventGet] = func(req request) (response, error) {
		start := binary.LittleEndian.Uint32(req.mbox[0:])
		minClass := int8(req.mbox[7])
		count := (req.dataLen - evtHeader) / evtSize
		var events []event
		for seq := start; seq <= newest && len(events) < count; seq++ {
			if c := classOf(seq); c >= minClass {
				events = append(events, event{Sequence: seq, Code: seq + 1000, Locale: 2, Class: c, Text: "event " + strconv.Itoa(int(seq))})
			}
		}
		return response{data: evtListBuf(events...)}, nil
	}
}

func allInfo(uint32) int8 { return classInfo }

func eventScraper(t *testing.T, dev *fakeDevice, store *memStore) *scraper {
	cfg := createDefaultConfig().(*Config)
	s, _, _ := newTestScraper(t, cfg, dev)
	s.events = &eventCursor{store: store}
	return s
}

func commitAll(t *testing.T, out output) {
	t.Helper()
	for _, c := range out.commit {
		require.NoError(t, c(context.Background()))
	}
}

func records(l plog.Logs) []plog.LogRecord {
	var out []plog.LogRecord
	for i := 0; i < l.ResourceLogs().Len(); i++ {
		sls := l.ResourceLogs().At(i).ScopeLogs()
		for j := 0; j < sls.Len(); j++ {
			rs := sls.At(j).LogRecords()
			for k := 0; k < rs.Len(); k++ {
				out = append(out, rs.At(k))
			}
		}
	}
	return out
}

func TestThreeEventsGiveThreeLogRecordsWithTheFourAttributes(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 13, func(seq uint32) int8 {
		return map[uint32]int8{11: classInfo, 12: classWarning, 13: classCritical}[seq]
	})
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("10")
	s := eventScraper(t, dev, store)

	out := s.scrape(context.Background())

	recs := records(out.logs)
	require.Len(t, recs, 3)
	assert.Equal(t, "event 11", recs[0].Body().Str())
	a := recs[1].Attributes()
	seq, _ := a.Get("raid.event.sequence")
	class, _ := a.Get("raid.event.class")
	code, _ := a.Get("raid.event.code")
	locale, _ := a.Get("raid.event.locale")
	assert.Equal(t, int64(12), seq.Int())
	assert.Equal(t, "warning", class.Str())
	assert.Equal(t, int64(1012), code.Int())
	assert.Equal(t, int64(2), locale.Int())
	assert.Equal(t, plog.SeverityNumberWarn, recs[1].SeverityNumber())
	assert.Equal(t, plog.SeverityNumberError, recs[2].SeverityNumber())
	id, _ := out.logs.ResourceLogs().At(0).Resource().Attributes().Get("hw.id")
	assert.Equal(t, "0000:03:00.0", id.Str())
	assert.Equal(t, uint32(10), store.seq(testCtrl), "the position moves only after delivery")
	commitAll(t, out)
	assert.Equal(t, uint32(13), store.seq(testCtrl))
}

func TestARestartResumesAfterTheSavedSequence(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 105, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("100")
	s := eventScraper(t, dev, store)

	out := s.scrape(context.Background())

	var starts []uint32
	for _, r := range dev.sent {
		if r.opcode == opEventGet {
			starts = append(starts, binary.LittleEndian.Uint32(r.mbox[0:]))
		}
	}
	assert.Equal(t, []uint32{101}, starts)
	assert.Len(t, records(out.logs), 5)
}

func TestTheFirstStartReadsNothingOldAndALaterBurstReadsInPages(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 500, allInfo)
	store := newMemStore()
	s := eventScraper(t, dev, store)

	out := s.scrape(context.Background())
	commitAll(t, out)

	assert.Empty(t, records(out.logs), "no old event is sent at the first start")
	assert.Zero(t, dev.count(opEventGet))
	assert.Equal(t, uint32(500), store.seq(testCtrl))

	eventLog(dev, 1000, func(seq uint32) int8 { return []int8{classDebug, classProgress, classInfo, classWarning}[seq%4] })
	var total int
	for i := 0; i < 5; i++ {
		page := s.scrape(context.Background())
		commitAll(t, page)
		n := len(records(page.logs))
		assert.LessOrEqual(t, n, 200, "at most 200 events are read in one scrape")
		total += n
		for _, r := range records(page.logs) {
			c, _ := r.Attributes().Get("raid.event.class")
			assert.NotEqual(t, "debug", c.Str())
			assert.NotEqual(t, "progress", c.Str())
		}
	}
	assert.Equal(t, uint32(1000), store.seq(testCtrl))
	assert.Equal(t, 250, total, "half of the 500 events are info or warning")
}

func TestTheLogHoldsNoEventsBelowTheChosenClass(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 20, func(seq uint32) int8 { return []int8{classInfo, classWarning, classCritical, classFatal}[seq%4] })
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("10")
	cfg := createDefaultConfig().(*Config)
	cfg.Events.MinClass = "critical"
	s, _, _ := newTestScraper(t, cfg, dev)
	s.events = &eventCursor{store: store}

	out := s.scrape(context.Background())

	for _, r := range records(out.logs) {
		c, _ := r.Attributes().Get("raid.event.class")
		assert.Contains(t, []string{"critical", "fatal"}, c.Str())
	}
	assert.NotEmpty(t, records(out.logs))
	commitAll(t, out)
	assert.Equal(t, uint32(20), store.seq(testCtrl), "a filtered log still moves the position to the newest event")
}

func TestAClearedLogResetsTheSavedSequenceAndReadsForward(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 40, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("900")
	s := eventScraper(t, dev, store)

	out := s.scrape(context.Background())
	commitAll(t, out)

	assert.Empty(t, records(out.logs))
	assert.Equal(t, uint32(40), store.seq(testCtrl))
	eventLog(dev, 42, allInfo)
	next := s.scrape(context.Background())
	assert.Len(t, records(next.logs), 2)
}

func TestNothingNewReadsNoEvents(t *testing.T) {
	dev := labFake(t)
	eventLog(dev, 7, allInfo)
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("7")
	s := eventScraper(t, dev, store)

	out := s.scrape(context.Background())

	assert.Zero(t, dev.count(opEventGet))
	assert.Empty(t, out.commit)
}

func TestEventAnswersThatAreShortOrRefusedAreSkipped(t *testing.T) {
	dev := labFake(t)
	dev.set(opEventGetInfo, make([]byte, 3))
	s := eventScraper(t, dev, newMemStore())
	assert.Empty(t, s.scrape(context.Background()).commit)

	dev2 := labFake(t)
	dev2.setStatus(opEventGetInfo, statusInvalidDCMD)
	s2 := eventScraper(t, dev2, newMemStore())
	assert.Empty(t, s2.scrape(context.Background()).commit)

	dev3 := labFake(t)
	dev3.set(opEventGetInfo, evtStateBuf(9, 1))
	dev3.set(opEventGet, make([]byte, 2))
	store := newMemStore()
	store.data[cursorKey(testCtrl)] = []byte("5")
	s3 := eventScraper(t, dev3, store)
	assert.Empty(t, s3.scrape(context.Background()).commit)

	dev4 := labFake(t)
	dev4.set(opEventGetInfo, evtStateBuf(9, 1))
	dev4.setStatus(opEventGet, 0x33)
	s4 := eventScraper(t, dev4, store)
	assert.Empty(t, s4.scrape(context.Background()).commit)
}

func TestCursorHelpersHandleBadValues(t *testing.T) {
	store := newMemStore()
	e := &eventCursor{store: store}
	_, ok, err := e.get(context.Background(), testCtrl)
	assert.False(t, ok)
	assert.NoError(t, err, "a missing key is not an error")
	store.data[cursorKey(testCtrl)] = []byte("not a number")
	_, ok, err = e.get(context.Background(), testCtrl)
	assert.False(t, ok)
	assert.ErrorIs(t, err, errCursorCorrupt)
	require.NoError(t, e.set(context.Background(), testCtrl, 12))
	v, ok, err := e.get(context.Background(), testCtrl)
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.Equal(t, uint32(12), v)
	store.failGet = errors.New("read failed")
	_, ok, err = e.get(context.Background(), testCtrl)
	assert.False(t, ok)
	assert.Error(t, err)
	store.failGet = nil
	store.failSet = errors.New("disk full")
	assert.Error(t, e.set(context.Background(), testCtrl, 13))
}

func TestClassLocaleMatchesTheCaptureToolConstants(t *testing.T) {
	assert.Equal(t, uint32(0xFE00FFFF), classLocale(classDebug), "capture tool: every class")
	assert.Equal(t, uint32(0x0000FFFF), classLocale(classInfo))
	assert.Equal(t, uint32(0x0100FFFF), classLocale(classWarning))
}

func TestSeverityOfEveryClass(t *testing.T) {
	want := map[int8]plog.SeverityNumber{
		classDebug: plog.SeverityNumberDebug, classProgress: plog.SeverityNumberDebug2, classInfo: plog.SeverityNumberInfo,
		classWarning: plog.SeverityNumberWarn, classCritical: plog.SeverityNumberError, classFatal: plog.SeverityNumberFatal, classDead: plog.SeverityNumberFatal,
	}
	for c, sev := range want {
		assert.Equal(t, sev, severity(c), className(c))
	}
}
