// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	emittedMetric   = "otelcol_fswatch_records_emitted"
	excludedMetric  = "otelcol_fswatch_records_excluded"
	vanishedMetric  = "otelcol_fswatch_vanished"
	overflowMetric  = "otelcol_fswatch_overflows"
	rescansMetric   = "otelcol_fswatch_rescans"
	cappedMetric    = "otelcol_fswatch_rescans_capped"
	timeoutsMetric  = "otelcol_fswatch_settle_timeouts"
	completedMetric = "otelcol_fswatch_writes_completed"
	droppedMetric   = "otelcol_fswatch_writes_dropped"
	scanSkipMetric  = "otelcol_fswatch_scan_skipped"
	oneSecond       = time.Second
	minuteAndSecond = 61 * time.Second
)

func TestLiveEventEmitsOneRecord(t *testing.T) {
	r := newRig(t)
	r.start()
	created := ago(r.clock, 3*time.Second)
	path := r.tree.put(filepath.Join("sub", "deep", "run.DX"), 0, created)
	top := r.tree.put("top.pdf", 2048, created.Add(time.Second))

	r.handle(r.added(filepath.Join("sub", "deep", "run.DX")))
	r.handle(r.added("top.pdf"))

	got := r.records()
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	first := got[0]
	if first.body != "created" || first.severity != plog.SeverityNumberInfo {
		t.Errorf("body %q severity %v", first.body, first.severity)
	}
	if !first.ts.Equal(created) || !first.obs.Equal(r.clock.Now()) {
		t.Errorf("ts %v obs %v, want %v and %v", first.ts, first.obs, created, r.clock.Now())
	}
	want := map[string]any{
		"file.path": path, "file.name": "run.DX", "file.extension": "DX", "file.size": int64(0),
		"file.directory": filepath.Dir(path), "log.source": "fswatch",
		"vstan.fswatch.dir_relative": filepath.Join("sub", "deep"), "vstan.fswatch.root": testRoot, "vstan.fswatch.catchup": false,
	}
	if !reflect.DeepEqual(first.attrs, want) {
		t.Errorf("attrs\n got %v\nwant %v", first.attrs, want)
	}
	if got[1].attrs["vstan.fswatch.dir_relative"] != "." || got[1].attrs["file.path"] != top || got[1].attrs["file.size"] != int64(2048) {
		t.Errorf("top-level file attrs: %v", got[1].attrs)
	}
}

func TestExtensionlessFileHasEmptyExtension(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("README", 1, ago(r.clock, time.Second))
	r.handle(r.added("README"))
	if got := r.records()[0].attrs["file.extension"]; got != "" {
		t.Fatalf("extension %q", got)
	}
}

func TestExcludedNamesProduceNoRecord(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("~a.tmp", 0, ago(r.clock, time.Second))
	r.tree.put("B.TMP", 0, ago(r.clock, time.Second))
	r.handle(r.added("~a.tmp"))
	r.handle(r.added("B.TMP"))
	if n := len(r.records()); n != 0 {
		t.Fatalf("want no records, got %d", n)
	}
	if got := r.counter(excludedMetric); got != 2 {
		t.Fatalf("excluded counter %d, want 2", got)
	}
}

func TestRenamePairs(t *testing.T) {
	tests := []struct {
		name   string
		events []rawEvent
		want   []string
	}{
		{
			"temp to final is new",
			[]rawEvent{{actionAdded, "~a.tmp"}, {actionRenamedOld, "~a.tmp"}, {actionRenamedNew, "a.dx"}},
			[]string{"a.dx"},
		},
		{
			"plain rename is ignored",
			[]rawEvent{{actionRenamedOld, "a.dx"}, {actionRenamedNew, "b.dx"}},
			nil,
		},
		{
			"excluded to excluded is ignored",
			[]rawEvent{{actionRenamedOld, "~a.tmp"}, {actionRenamedNew, "~b.tmp"}},
			nil,
		},
		{
			"unpaired new name is ignored",
			[]rawEvent{{actionRenamedNew, "b.dx"}},
			nil,
		},
		{
			"removed and modified are ignored",
			[]rawEvent{{actionRemoved, "a.dx"}, {actionModified, "a.dx"}, {actionOther, "a.dx"}},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.start()
			for _, n := range []string{"a.dx", "b.dx", "~b.tmp"} {
				r.tree.put(n, 1, ago(r.clock, time.Second))
			}
			r.handle(watchMsg{events: tt.events})
			if got := names(r.records()); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenamePairAcrossMessages(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(watchMsg{events: []rawEvent{{actionRenamedOld, "~a.tmp"}}})
	r.handle(watchMsg{events: []rawEvent{{actionRenamedNew, "a.dx"}}})
	if got := names(r.records()); !reflect.DeepEqual(got, []string{"a.dx"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDirectoryMovedInEmitsItsFiles(t *testing.T) {
	r := newRig(t)
	r.start()
	old := ago(r.clock, 400*time.Hour)
	r.tree.putDir("run1")
	for i := 1; i <= 3; i++ {
		r.tree.put(filepath.Join("run1", fmt.Sprintf("f%d.dx", i)), 10, old.Add(time.Duration(i)*time.Second))
	}
	r.tree.put(filepath.Join("run1", "~lock.tmp"), 0, old)
	r.handle(r.added("run1"))

	got := r.records()
	if len(got) != 3 {
		t.Fatalf("want 3 records for the files, got %d: %v", len(got), names(got))
	}
	for _, rec := range got {
		if rec.attrs["vstan.fswatch.dir_relative"] != "run1" || rec.attrs["vstan.fswatch.catchup"] != false {
			t.Errorf("attrs %v", rec.attrs)
		}
	}
}

func TestVanishedFileIsCountedNotEmitted(t *testing.T) {
	r := newRig(t)
	r.start()
	r.handle(r.added("gone.dx"))
	if len(r.records()) != 0 || r.counter(vanishedMetric) != 1 {
		t.Fatalf("records %d vanished %d", len(r.records()), r.counter(vanishedMetric))
	}
}

func TestStatFailureEmitsWithoutSize(t *testing.T) {
	r := newRig(t)
	r.start()
	p := r.tree.put("locked.dx", 5, ago(r.clock, time.Hour))
	r.tree.statErr[p] = fs.ErrPermission
	r.handle(r.added("locked.dx"))
	got := r.records()
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if _, has := got[0].attrs["file.size"]; has {
		t.Errorf("file.size must be absent: %v", got[0].attrs)
	}
	if !got[0].ts.Equal(r.clock.Now()) {
		t.Errorf("timestamp %v, want the observed time", got[0].ts)
	}
}

func TestSamePathAndCreationTimeIsEmittedOnce(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	r.handle(r.added("a.dx"))
	r.handle(watchMsg{events: []rawEvent{{actionAdded, "A.DX"}}})
	if n := len(r.records()); n != 1 {
		t.Fatalf("want 1 record, got %d", n)
	}
}

func TestRecreatedFileIsANewRecord(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, 2*time.Second))
	r.handle(r.added("a.dx"))
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	if n := len(r.records()); n != 2 {
		t.Fatalf("want 2 records, got %d", n)
	}
}

func TestThousandFilesNoDuplicatePath(t *testing.T) {
	r := newRig(t)
	r.start()
	base := ago(r.clock, 10*time.Second)
	var events []rawEvent
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("f%04d.dx", i)
		r.tree.put(name, 1, base.Add(time.Duration(i)*time.Millisecond))
		events = append(events, rawEvent{actionAdded, name})
	}
	r.handle(watchMsg{events: events})
	r.handle(watchMsg{events: events})
	got := r.records()
	seen := map[string]bool{}
	for _, rec := range got {
		seen[rec.attrs["file.path"].(string)] = true
	}
	if len(got) != 1000 || len(seen) != 1000 {
		t.Fatalf("records %d unique %d", len(got), len(seen))
	}
}

func withSettle(settle, timeout time.Duration) rigOption {
	return withConfig(func(c *Config) { c.Settle, c.SettleTimeout = settle, timeout })
}

func TestWriteCompleteRecordFollowsCreationAfterStableSize(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	created := ago(r.clock, time.Second)
	r.tree.put("grow.dx", 10, created)
	r.handle(r.added("grow.dx"))
	got := r.records()
	if len(got) != 1 || got[0].body != logBody || got[0].attrs["file.size"] != int64(10) {
		t.Fatalf("the creation record goes out at once, got %+v", got)
	}

	for i := int64(1); i <= 5; i++ {
		r.clock.Advance(oneSecond)
		r.tree.put("grow.dx", 10+i*100, created)
		r.tick()
	}
	r.clock.Advance(oneSecond)
	r.tick()
	if n := len(r.records()); n != 1 {
		t.Fatalf("a growing file and one stable second give no second record, got %d records", n)
	}

	r.clock.Advance(oneSecond)
	r.tick()
	got = r.records()
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	done := got[1]
	if !isComplete(done.body) || done.attrs["file.size"] != int64(510) {
		t.Errorf("write-complete record %+v", done)
	}
	if !done.ts.Equal(r.clock.Now()) || r.counter(completedMetric) != 1 {
		t.Errorf("timestamp %v, completed %d", done.ts, r.counter(completedMetric))
	}

	r.clock.Advance(time.Minute)
	r.tick()
	if n := len(r.records()); n != 2 {
		t.Fatalf("a completed file is not reported again, got %d records", n)
	}
}

func TestFileStillChangingAtSettleTimeoutGetsSecondRecordWithoutFinalSize(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, 5*time.Second))
	r.start()
	created := ago(r.clock, time.Second)
	r.tree.put("busy.dx", 1, created)
	r.handle(r.added("busy.dx"))
	for i := int64(1); i <= 8; i++ {
		r.clock.Advance(oneSecond)
		r.tree.put("busy.dx", 1+i, created)
		r.tick()
	}
	got := r.records()
	if len(got) != 2 || !isComplete(got[1].body) {
		t.Fatalf("want the creation and the write-complete record, got %d", len(got))
	}
	if r.counter(timeoutsMetric) != 1 || r.counter(completedMetric) != 1 {
		t.Errorf("settle_timeouts %d, completed %d", r.counter(timeoutsMetric), r.counter(completedMetric))
	}
}

func TestWatchedFileThatVanishesGetsNoSecondRecord(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	r.tree.put("x.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("x.dx"))
	r.tree.remove("x.dx")
	r.clock.Advance(3 * oneSecond)
	r.tick()
	if len(r.records()) != 1 || r.counter(vanishedMetric) != 1 {
		t.Fatalf("records %d vanished %d", len(r.records()), r.counter(vanishedMetric))
	}
}

func TestFailedWriteCompleteDeliveryIsRetried(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	r.tree.put("p.dx", 7, ago(r.clock, time.Second))
	r.handle(r.added("p.dx"))
	r.sink.fail.Store(true)
	r.clock.Advance(3 * oneSecond)
	r.tick()
	if n := len(r.records()); n != 1 || r.counter(completedMetric) != 0 {
		t.Fatalf("records %d completed %d", n, r.counter(completedMetric))
	}
	if r.rw.frozen {
		t.Fatal("a failed write-complete record must not hold the sentinel")
	}
	r.sink.fail.Store(false)
	r.clock.Advance(oneSecond)
	r.tick()
	got := r.records()
	if len(got) != 2 || !isComplete(got[1].body) {
		t.Fatalf("want the retried write-complete record, got %d records", len(got))
	}
}

func TestCaughtUpFileGetsTheSameSecondRecord(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute), withConfig(func(c *Config) { c.StartAt = "beginning" }))
	r.tree.put("old.dx", 5, ago(r.clock, time.Hour))
	r.start()
	r.clock.Advance(3 * oneSecond)
	r.tick()
	got := r.records()
	if len(got) != 2 || !isComplete(got[1].body) || got[1].attrs["file.size"] != int64(5) || got[1].attrs["vstan.fswatch.catchup"] != true {
		t.Fatalf("want a creation and a write-complete record for the caught-up file, got %+v", got)
	}
}

func TestCloseSendsNothingForWatchedFiles(t *testing.T) {
	r := newRig(t, withSettle(time.Minute, time.Hour))
	r.start()
	r.tree.put("p.dx", 7, ago(r.clock, time.Second))
	r.handle(r.added("p.dx"))
	r.rw.close(t.Context())
	if n := len(r.records()); n != 1 {
		t.Fatalf("want only the creation record, got %d", n)
	}
}

func TestWriteCompleteDeliveryGivesUpAfterTenAttempts(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Hour))
	r.start()
	r.tree.put("p.dx", 7, ago(r.clock, time.Second))
	r.handle(r.added("p.dx"))
	r.sink.fail.Store(true)
	r.clock.Advance(3 * oneSecond)
	for i := 1; i <= 9; i++ {
		r.tick()
		r.clock.Advance(oneSecond)
	}
	if r.counter(droppedMetric) != 0 {
		t.Fatalf("dropped after 9 attempts: %d", r.counter(droppedMetric))
	}
	r.tick()
	if r.counter(droppedMetric) != 1 || r.warnings("gave up") != 1 {
		t.Fatalf("after 10 attempts dropped %d, gave-up warnings %d", r.counter(droppedMetric), r.warnings("gave up"))
	}
	r.sink.fail.Store(false)
	r.clock.Advance(oneSecond)
	r.tick()
	if n := len(r.records()); n != 1 || r.counter(completedMetric) != 0 {
		t.Fatalf("a dropped write-complete record must not come back: records %d", n)
	}
}

func TestWriteCompleteDeliveryGivesUpAfterSettleTimeoutOfFailures(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	r.tree.put("p.dx", 7, ago(r.clock, time.Second))
	r.handle(r.added("p.dx"))
	r.sink.fail.Store(true)
	r.clock.Advance(3 * oneSecond)
	r.tick()
	if r.counter(droppedMetric) != 0 {
		t.Fatal("one failure must not drop the record")
	}
	r.clock.Advance(minuteAndSecond)
	r.tick()
	if r.counter(droppedMetric) != 1 || r.warnings("gave up") != 1 {
		t.Fatalf("dropped %d, gave-up warnings %d", r.counter(droppedMetric), r.warnings("gave up"))
	}
}

// reportedBeforeRestart returns a restarted rig whose saved state holds a.dx, reported 31 s ago.
func reportedBeforeRestart(t *testing.T, modifiedAgo time.Duration) *rig {
	t.Helper()
	r := newRig(t, withSettle(2*time.Second, 10*time.Minute))
	r.start()
	r.clock.Advance(10 * oneSecond)
	created := ago(r.clock, oneSecond)
	r.tree.putModified("a.dx", 5, created, created)
	r.handle(r.added("a.dx"))
	r.rw.close(t.Context())
	r.clock.Advance(31 * oneSecond)
	r.tree.putModified("a.dx", 5, created, ago(r.clock, modifiedAgo))
	return r.restart()
}

func TestRestartChecksRecentFilesAndSendsStableOnes(t *testing.T) {
	down := reportedBeforeRestart(t, 30*time.Second)
	down.start()
	down.tick()
	got := down.records()
	if len(got) != 2 || !isComplete(got[1].body) || got[1].attrs["file.size"] != int64(5) || got[1].attrs["vstan.fswatch.catchup"] != true {
		t.Fatalf("want the creation record and a catch-up write-complete record, got %+v", got)
	}
	down.tick()
	down.handle(watchMsg{overflow: true})
	down.clock.Advance(minuteAndSecond)
	down.tick()
	if n := len(down.records()); n != 2 {
		t.Fatalf("a rescan must not check files again, got %d records", n)
	}
}

func TestRestartDoesNotRepeatAFileWhoseSecondRecordIsOut(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, 10*time.Minute))
	r.start()
	r.clock.Advance(10 * oneSecond)
	created := ago(r.clock, oneSecond)
	r.tree.putModified("a.dx", 5, created, created)
	r.handle(r.added("a.dx"))
	r.clock.Advance(3 * oneSecond)
	r.tick()
	if n := len(r.records()); n != 2 {
		t.Fatalf("want both records before the restart, got %d", n)
	}
	r.rw.close(t.Context())
	r.clock.Advance(oneSecond)
	down := r.restart()
	down.start()
	down.clock.Advance(oneSecond)
	down.tick()
	if n := len(down.records()); n != 2 {
		t.Fatalf("a finished file must not get a third record, got %d", n)
	}
}

func TestRestartKeepsWatchingAFileStillBeingWritten(t *testing.T) {
	down := reportedBeforeRestart(t, oneSecond)
	down.start()
	down.tick()
	if n := len(down.records()); n != 1 {
		t.Fatalf("a file written a second ago is not stable yet, got %d records", n)
	}
	down.clock.Advance(3 * oneSecond)
	down.tick()
	if got := down.records(); len(got) != 2 || !isComplete(got[1].body) {
		t.Fatalf("want the write-complete record once the size held still, got %d records", len(got))
	}
}

func TestRestartSkipsFilesOlderThanSettleTimeout(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, time.Minute))
	r.start()
	r.clock.Advance(10 * oneSecond)
	created := ago(r.clock, oneSecond)
	r.tree.putModified("a.dx", 5, created, created)
	r.handle(r.added("a.dx"))
	r.rw.close(t.Context())
	r.clock.Advance(2 * time.Minute)
	down := r.restart()
	down.start()
	down.tick()
	if n := len(down.records()); n != 1 {
		t.Fatalf("a file reported longer ago than settle_timeout is not checked, got %d records", n)
	}
}

func TestFirstStartChecksNothing(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, 10*time.Minute))
	created := ago(r.clock, 5*time.Second)
	r.tree.putModified("a.dx", 5, created, created)
	r.start()
	r.clock.Advance(10 * oneSecond)
	r.tick()
	if n := len(r.records()); n != 0 {
		t.Fatalf("a first start at end reports nothing, got %d records", n)
	}
}

func TestStartAtBeginningSendsOneRecordForAFileAlreadyStable(t *testing.T) {
	r := newRig(t, withSettle(2*time.Second, 10*time.Minute), withConfig(func(c *Config) { c.StartAt = "beginning" }))
	old := ago(r.clock, time.Hour)
	r.tree.putModified("old.dx", 9, old, old)
	young := ago(r.clock, oneSecond)
	r.tree.putModified("young.dx", 3, young, young)
	r.start()
	r.tick()
	got := r.records()
	if len(got) != 2 || got[0].body != logBody || got[0].attrs["file.size"] != int64(9) || got[1].body != logBody {
		t.Fatalf("want one record with the final size for the old file and a plain one for the young file, got %+v", got)
	}

	r.clock.Advance(3 * oneSecond)
	r.tick()
	got = r.records()
	if len(got) != 3 || !isComplete(got[2].body) || got[2].attrs["file.name"] != "young.dx" || got[2].attrs["file.size"] != int64(3) {
		t.Fatalf("the young file takes the two-record path, got %+v", got)
	}

	down := r.restart()
	down.start()
	down.tick()
	if n := len(down.records()); n != 3 {
		t.Fatalf("files already reported are skipped, got %d records", n)
	}
}

func TestFirstStartAtEndEmitsNoHistory(t *testing.T) {
	r := newRig(t)
	r.tree.put("old1.dx", 1, ago(r.clock, time.Hour))
	r.tree.put("old2.dx", 1, ago(r.clock, 100*time.Hour))
	r.start()
	if n := len(r.records()); n != 0 {
		t.Fatalf("want no history, got %d records", n)
	}
	st := r.store.saved(t, testRoot)
	if !time.Unix(0, st.Sentinel).Equal(r.clock.Now()) {
		t.Errorf("sentinel %v, want now", time.Unix(0, st.Sentinel))
	}
}

func TestFirstStartBeginningEmitsEverythingIgnoringMaxCatchup(t *testing.T) {
	r := newRig(t, withConfig(func(c *Config) { c.StartAt = "beginning" }))
	for i := 1; i <= 3; i++ {
		r.tree.put(fmt.Sprintf("old%d.dx", i), 1, ago(r.clock, time.Duration(300+i)*time.Hour))
	}
	r.start()
	got := r.records()
	if len(got) != 3 {
		t.Fatalf("want 3 records, got %d", len(got))
	}
	for _, rec := range got {
		if rec.attrs["vstan.fswatch.catchup"] != true {
			t.Errorf("catchup %v", rec.attrs["vstan.fswatch.catchup"])
		}
	}
	if r.warnings("max_catchup") != 0 {
		t.Error("beginning must not warn about max_catchup")
	}
}

// restart builds a second receiver over the same store, tree and sink.
func (r *rig) restart() *rig {
	next := newRig(r.t, func(n *rig) { n.cfg = r.cfg })
	next.store, next.tree, next.sink, next.clock = r.store, r.tree, r.sink, r.clock
	spec := r.cfg.rootSpecs()[0]
	m, _ := newMatcher(spec.patterns())
	next.rw = newRootWatch(rootParams{
		cfg: r.cfg, root: testRoot, name: spec.name, logger: next.rw.logger, next: r.sink, tel: next.rw.tel,
		tree: r.tree, store: r.store, now: r.clock.Now, match: m,
	})
	return next
}

func TestCatchUpAfterRestart(t *testing.T) {
	r := newRig(t)
	r.start()
	r.clock.Advance(time.Minute)
	r.rw.close(t.Context())

	down := r.restart()
	for i := 1; i <= 3; i++ {
		down.tree.put(fmt.Sprintf("while-down%d.dx", i), 1, r.clock.Now().Add(time.Duration(i)*time.Second))
	}
	r.clock.Advance(time.Minute)
	down.start()

	got := records(down.sink)
	if len(got) != 3 {
		t.Fatalf("want 3 catch-up records, got %d", len(got))
	}
	for i, rec := range got {
		if rec.attrs["vstan.fswatch.catchup"] != true {
			t.Errorf("record %d catchup %v", i, rec.attrs["vstan.fswatch.catchup"])
		}
	}
	if !reflect.DeepEqual(names(got), []string{"while-down1.dx", "while-down2.dx", "while-down3.dx"}) {
		t.Errorf("order %v", names(got))
	}

	down.rw.close(t.Context())
	again := down.restart()
	r.clock.Advance(time.Minute)
	again.start()
	if n := len(records(again.sink)); n != 3 {
		t.Fatalf("a restart with no new files must add nothing, got %d records", n)
	}
}

func TestCatchUpIsLimitedByMaxCatchup(t *testing.T) {
	r := newRig(t)
	r.start()
	r.rw.close(t.Context())
	r.clock.Advance(10 * 24 * time.Hour)

	down := r.restart()
	down.tree.put("too-old.dx", 1, ago(r.clock, 9*24*time.Hour))
	down.tree.put("recent.dx", 1, ago(r.clock, 24*time.Hour))
	down.start()

	if got := names(records(down.sink)); !reflect.DeepEqual(got, []string{"recent.dx"}) {
		t.Fatalf("got %v", got)
	}
	if down.warnings("max_catchup") != 1 {
		t.Fatalf("want one max_catchup warning, got %d", down.warnings("max_catchup"))
	}
}

func TestSentinelIsStoredUnderTheRootKey(t *testing.T) {
	r := newRig(t)
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if _, ok := r.store.data[`fswatch/\cds`]; !ok {
		t.Fatalf("keys %v", func() []string {
			var k []string
			for key := range r.store.data {
				k = append(k, key)
			}
			return k
		}())
	}
	st := r.store.saved(t, testRoot)
	if st.V != 1 || len(st.Recent) != 1 {
		t.Fatalf("state %+v", st)
	}
}

func TestRecentSetSurvivesRestart(t *testing.T) {
	r := newRig(t)
	r.start()
	r.clock.Advance(time.Minute)
	created := ago(r.clock, time.Second)
	r.tree.put("a.dx", 1, created)
	r.handle(r.added("a.dx"))
	r.rw.close(t.Context())

	again := r.restart()
	again.start()
	if n := len(records(again.sink)); n != 1 {
		t.Fatalf("the file must not come back after a restart, got %d records", n)
	}
}

func TestOverflowRescansAndEmitsLostFiles(t *testing.T) {
	r := newRig(t)
	r.start()
	r.clock.Advance(time.Minute)
	base := ago(r.clock, 30*time.Second)
	for i := 0; i < 2000; i++ {
		r.tree.put(fmt.Sprintf("burst%04d.dx", i), 1, base.Add(time.Duration(i)*time.Millisecond))
	}
	r.handle(watchMsg{overflow: true})
	got := r.records()
	if len(got) != 2000 {
		t.Fatalf("want 2000 records, got %d", len(got))
	}
	if got[0].attrs["vstan.fswatch.catchup"] != true {
		t.Error("rescan records are catch-up records")
	}
	if r.counter(overflowMetric) != 1 || r.counter(rescansMetric) != 1 || r.warnings("overflow") != 1 {
		t.Errorf("overflows %d rescans %d warnings %d", r.counter(overflowMetric), r.counter(rescansMetric), r.warnings("overflow"))
	}
}

func TestRescansAreSpacedAndCapped(t *testing.T) {
	r := newRig(t, withConfig(func(c *Config) { c.MaxRescans = 2 }))
	r.start()
	for i := 0; i < 2; i++ {
		r.handle(watchMsg{overflow: true})
		r.clock.Advance(minuteAndSecond)
	}
	if r.counter(rescansMetric) != 2 {
		t.Fatalf("rescans %d, want 2", r.counter(rescansMetric))
	}
	r.handle(watchMsg{overflow: true})
	r.clock.Advance(minuteAndSecond)
	r.tick()
	if r.counter(rescansMetric) != 2 || r.counter(cappedMetric) < 1 {
		t.Fatalf("rescans %d capped %d", r.counter(rescansMetric), r.counter(cappedMetric))
	}
	r.tick()
	if r.counter(cappedMetric) != 1 {
		t.Fatalf("the counter counts one hold per hour, got %d", r.counter(cappedMetric))
	}
	if r.warnings("rescan limit") != 1 {
		t.Fatalf("want one rescan-limit warning per hour, got %d", r.warnings("rescan limit"))
	}
	r.clock.Advance(time.Hour)
	r.tick()
	if r.counter(rescansMetric) != 3 {
		t.Fatalf("after an hour the rescan is allowed again, rescans %d", r.counter(rescansMetric))
	}
}

func TestFailedLaterBatchKeepsSentinelBelowUnsentFiles(t *testing.T) {
	r := newRig(t)
	r.start()
	r.clock.Advance(10 * oneSecond)
	newer, older := ago(r.clock, oneSecond), ago(r.clock, 5*oneSecond)
	r.tree.putDir("d")
	// Walk order is by name: the 500 newer files come first, the 100 older ones last.
	for i := 0; i < 500; i++ {
		r.tree.put(fmt.Sprintf("d/a%03d.dx", i), 1, newer)
	}
	for i := 0; i < 100; i++ {
		r.tree.put(fmt.Sprintf("d/b%03d.dx", i), 1, older)
	}

	r.sink.failAt.Store(2)
	r.handle(r.added("d"))
	if !r.rw.sentinel.Before(newer) {
		t.Fatalf("sentinel %v reached the files of the failed batch (created %v)", r.rw.sentinel, newer)
	}

	r.sink.failAt.Store(0)
	r.clock.Advance(minuteAndSecond)
	r.tick()
	seen := map[string]int{}
	for _, name := range names(r.records()) {
		seen[name]++
	}
	if len(seen) != 600 {
		t.Fatalf("%d distinct files reported, want 600", len(seen))
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("%s reported %d times", name, n)
		}
	}
}

func TestRescanWaitsAtLeastAMinuteBetweenRuns(t *testing.T) {
	r := newRig(t)
	r.start()
	r.handle(watchMsg{overflow: true})
	r.clock.Advance(10 * oneSecond)
	r.handle(watchMsg{overflow: true})
	if r.counter(rescansMetric) != 1 {
		t.Fatalf("rescans %d, want 1", r.counter(rescansMetric))
	}
	r.tree.put("late.dx", 1, ago(r.clock, 5*time.Second))
	r.clock.Advance(minuteAndSecond)
	r.tick()
	if r.counter(rescansMetric) != 2 || len(r.records()) != 1 {
		t.Fatalf("rescans %d records %d", r.counter(rescansMetric), len(r.records()))
	}
}

func TestSentinelFreezesWhileConsumerFails(t *testing.T) {
	r := newRig(t)
	r.start()
	before := r.store.saved(t, testRoot).Sentinel
	r.clock.Advance(10 * oneSecond)
	r.sink.fail.Store(true)
	created := ago(r.clock, time.Second)
	r.tree.put("blocked.dx", 1, created)
	r.handle(r.added("blocked.dx"))
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if len(r.records()) != 0 {
		t.Fatal("nothing can be delivered while the consumer fails")
	}
	if got := r.store.saved(t, testRoot).Sentinel; got >= created.UnixNano() || got != before {
		t.Fatalf("sentinel moved to %d (file %d, before %d)", got, created.UnixNano(), before)
	}

	r.sink.fail.Store(false)
	r.clock.Advance(minuteAndSecond)
	r.tick()
	got := r.records()
	if len(got) != 1 || got[0].attrs["vstan.fswatch.catchup"] != true {
		t.Fatalf("want the file once after recovery, got %v", got)
	}
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if st := r.store.saved(t, testRoot); st.Sentinel < created.UnixNano() {
		t.Fatalf("sentinel %d must pass the file %d after recovery", st.Sentinel, created.UnixNano())
	}
	r.clock.Advance(minuteAndSecond)
	r.tick()
	if len(r.records()) != 1 {
		t.Fatalf("emitted again: %d records", len(r.records()))
	}
}

func TestSentinelPassesAFileStillBeingWritten(t *testing.T) {
	r := newRig(t, withSettle(5*time.Second, time.Hour))
	r.start()
	r.clock.Advance(10 * oneSecond)
	created := ago(r.clock, 2*time.Second)
	r.tree.put("growing.dx", 1, created)
	r.handle(r.added("growing.dx"))
	for i := int64(1); i <= 3; i++ {
		r.clock.Advance(oneSecond)
		r.tree.put("growing.dx", 1+i, created)
		r.tick()
	}
	if got := r.store.saved(t, testRoot).Sentinel; got < created.UnixNano() {
		t.Fatalf("sentinel %d must pass a file whose creation record is out (%d)", got, created.UnixNano())
	}
	if got := names(r.records()); !reflect.DeepEqual(got, []string{"growing.dx"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFutureDatedFileIsEmittedLiveAndSkippedByScans(t *testing.T) {
	r := newRig(t)
	r.start()
	future := r.clock.Now().Add(time.Hour)
	r.tree.put("future.dx", 1, future)
	r.handle(r.added("future.dx"))
	if len(r.records()) != 1 {
		t.Fatalf("a live event is emitted, got %d records", len(r.records()))
	}
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if got := r.store.saved(t, testRoot).Sentinel; got > r.clock.Now().UnixNano() {
		t.Fatalf("sentinel %d is ahead of the clock %d", got, r.clock.Now().UnixNano())
	}

	other := newRig(t)
	other.tree.put("future2.dx", 1, other.clock.Now().Add(time.Hour))
	other.cfg.StartAt = "beginning"
	other.start()
	if len(other.records()) != 0 || other.counter(scanSkipMetric, "reason", "future") != 1 {
		t.Fatalf("records %d skipped %d", len(other.records()), other.counter(scanSkipMetric, "reason", "future"))
	}
}

func TestDeniedDirectoriesAreCountedOncePerScan(t *testing.T) {
	r := newRig(t)
	r.tree.denied = 3
	r.start()
	if r.counter(scanSkipMetric, "reason", "denied") != 3 || r.warnings("denied") != 1 {
		t.Fatalf("after the first scan: skipped %d warnings %d", r.counter(scanSkipMetric, "reason", "denied"), r.warnings("denied"))
	}
	r.handle(watchMsg{overflow: true})
	if r.counter(scanSkipMetric, "reason", "denied") != 6 || r.warnings("denied") != 2 {
		t.Fatalf("after the rescan: skipped %d warnings %d", r.counter(scanSkipMetric, "reason", "denied"), r.warnings("denied"))
	}
}

func TestStorageFailuresDegradeToMemory(t *testing.T) {
	r := newRig(t)
	r.store.getErr = errors.New("disk gone")
	r.store.setErr = errors.New("disk gone")
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	r.clock.Advance(2 * oneSecond)
	r.tick()
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if len(r.records()) != 1 {
		t.Fatalf("records %d", len(r.records()))
	}
	if r.warnings("storage") != 2 {
		t.Fatalf("want one read warning and one write warning, got %d", r.warnings("storage"))
	}
}

func TestCorruptStateIsTreatedAsFirstStart(t *testing.T) {
	r := newRig(t)
	r.store.data[rootKey(testRoot)] = []byte("{not json")
	r.tree.put("old.dx", 1, ago(r.clock, time.Hour))
	r.start()
	if len(r.records()) != 0 || r.warnings("state") != 1 {
		t.Fatalf("records %d warnings %d", len(r.records()), r.warnings("state"))
	}
}

func TestRescanIntervalFindsMissedFiles(t *testing.T) {
	r := newRig(t, withConfig(func(c *Config) { c.RescanInterval = 5 * time.Minute }))
	r.start()
	r.tree.put("missed.dx", 1, ago(r.clock, time.Second))
	r.clock.Advance(4 * time.Minute)
	r.tick()
	if len(r.records()) != 0 {
		t.Fatal("too early for the interval scan")
	}
	r.clock.Advance(2 * time.Minute)
	r.tick()
	if got := names(r.records()); !reflect.DeepEqual(got, []string{"missed.dx"}) {
		t.Fatalf("got %v", got)
	}
}

func TestStorageAbsentWarnsOnce(t *testing.T) {
	r := newRig(t)
	r.rw.store = nil
	r.start()
	r.tree.put("a.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("a.dx"))
	r.clock.Advance(2 * oneSecond)
	r.tick()
	if len(r.records()) != 1 {
		t.Fatalf("records %d", len(r.records()))
	}
}

func TestNextDelay(t *testing.T) {
	r := newRig(t, withConfig(func(c *Config) { c.Settle = 4 * time.Second }))
	r.start()
	if d := r.rw.nextDelay(); d != 0 {
		t.Fatalf("an idle receiver needs no timer, got %v", d)
	}
	r.tree.put("p.dx", 1, ago(r.clock, time.Second))
	r.handle(r.added("p.dx"))
	if d := r.rw.nextDelay(); d != oneSecond {
		t.Fatalf("pending settle polls at settle/4, got %v", d)
	}
}
