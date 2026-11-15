// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvncprocessor

import (
	"context"
	"maps"
	"testing"
	"time"
	_ "time/tzdata"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Sample lines from a LogLevel 9 server log, with documentation addresses in place of real ones.
const (
	lineIncoming      = "[ 6020/ 6908] 2026-11-13 20:43:06:720 + Incoming rfb connection from 192.0.2.10 to port 5900"
	lineConnected     = "[ 6020/ 6908] 2026-11-13 20:43:06:720 ! Client #0 connected"
	lineInitPhase     = "[ 6020/19112] 2026-11-13 20:43:06:720 - Entering RFB initialization phase 1"
	lineAuthenticated = "[ 6020/19112] 2026-11-13 20:43:06:807   Authenticated with view-only password = 0"
	lineIdleTimeout   = "[ 6020/ 6908] 2026-11-13 20:43:06:720 ! Set socket idle timeout, 0 ms"
	lineBuild         = "[ 6020/ 6484] 2026-11-10 21:47:52:697 + TightVNC Server Build on Jun  3 2026 at 14:25:03"
	lineInitWinSock   = "[ 6020/ 6484] 2026-11-10 21:47:52:740 - Initialize WinSock"
	lineStartMain     = "[ 6020/ 6484] 2026-11-10 21:47:52:741 + Starting main RFB server"
	lineServerStarted = "[ 6020/ 6484] 2026-11-10 21:47:52:756 + Rfb server started at 192.0.2.1:5900"
	lineStopping      = "[ 6020/ 6484] 2026-11-10 21:47:52:756 + Stopping control server"
	lineException     = `[ 6020/16816] 2026-11-10 21:44:08:000 ! Exception in control client thread: ` +
		`"The Pipe's read function failed after GetOverlappedResult calling (pipe handle is 123) ` +
		`(The pipe has been ended. (109)"`
)

var keepSamples = []string{
	lineIncoming, lineConnected, lineInitPhase, lineAuthenticated, lineIdleTimeout, lineBuild,
	lineInitWinSock, lineStartMain, lineServerStarted, lineStopping, lineException,
}

var noiseSamples = []string{
	"[ 6020/16816] 2026-11-11 11:16:46:375 : Control client sends process ID",
	"[ 6020/16816] 2026-11-11 11:16:46:375   Recieved control message ID 20, size 0",
	"[ 6020/16816] 2026-11-11 11:16:46:375 : Control client requests tray icon visibility flag",
	"[ 6020/16816] 2026-11-11 11:16:46:380 : Control client requests client list",
	"[ 6020/16816] 2026-11-11 11:16:47:400   process memory usage: 12734464",
	"[ 6020/ 6908] 2026-11-13 20:43:06:720   new client, process memory usage: 12832768",
	"[ 6020/19112] 2026-11-13 20:43:06:807   Shared flag = 1",
	"[ 6020/19112] 2026-11-13 20:43:06:807   Initial view-only state = 0",
	"[ 6020/19112] 2026-11-13 20:43:06:807   RFB initialization phase 1 completed",
	"[ 6020/ 6484] 2026-11-10 21:47:52:756 : Requested to start up extra RFB servers",
	"[ 6020/ 6484] 2026-11-10 21:47:52:756 : Requested to shut down extra RFB servers",
	"[ 6020/ 6484] 2026-11-10 21:47:52:756 : Considering to reload extra RFB servers",
	"[ 6020/ 6484] 2026-11-10 21:47:52:756 : Same Extra Ports configuration = 0, enough servers = 1",
}

var observed = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// stamped builds a line from the prefix and the text after the timestamp, which carries its own separator.
func stamped(rest string) string {
	return "[ 6020/ 6908] 2026-11-13 20:43:06:720" + rest
}

func utcConfig() *Config {
	cfg := defaultConfig()
	cfg.TimeZone = "UTC"
	return cfg
}

func newLogs(bodies ...string) plog.Logs {
	ld := plog.NewLogs()
	records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, body := range bodies {
		lr := records.AppendEmpty()
		lr.Body().SetStr(body)
		lr.SetObservedTimestamp(pcommon.NewTimestampFromTime(observed))
	}
	return ld
}

func process(t *testing.T, cfg *Config, ld plog.Logs, set processor.Settings) []plog.LogRecord {
	t.Helper()
	sink := new(consumertest.LogsSink)
	proc, err := NewFactory().CreateLogs(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	var out []plog.LogRecord
	for _, batch := range sink.AllLogs() {
		for ri := 0; ri < batch.ResourceLogs().Len(); ri++ {
			scopes := batch.ResourceLogs().At(ri).ScopeLogs()
			for si := 0; si < scopes.Len(); si++ {
				records := scopes.At(si).LogRecords()
				for i := 0; i < records.Len(); i++ {
					out = append(out, records.At(i))
				}
			}
		}
	}
	return out
}

func run(t *testing.T, cfg *Config, bodies ...string) []plog.LogRecord {
	t.Helper()
	return process(t, cfg, newLogs(bodies...), processortest.NewNopSettings(component.MustNewType(typeStr)))
}

func runOne(t *testing.T, body string) plog.LogRecord {
	t.Helper()
	got := run(t, utcConfig(), body)
	if len(got) != 1 {
		t.Fatalf("kept %d records for %q, want 1", len(got), body)
	}
	return got[0]
}

func attrString(lr plog.LogRecord, key string) string {
	if v, ok := lr.Attributes().Get(key); ok {
		return v.Str()
	}
	return "<absent>"
}

func attrInt(lr plog.LogRecord, key string) int64 {
	if v, ok := lr.Attributes().Get(key); ok {
		return v.Int()
	}
	return -1
}

func TestKeptSamples_BodyIsTheMessageWithoutThePrefix(t *testing.T) {
	cases := map[string]string{
		lineIncoming:      "Incoming rfb connection from 192.0.2.10 to port 5900",
		lineConnected:     "Client #0 connected",
		lineInitPhase:     "Entering RFB initialization phase 1",
		lineAuthenticated: "Authenticated with view-only password = 0",
		lineBuild:         "TightVNC Server Build on Jun  3 2026 at 14:25:03",
	}
	for line, want := range cases {
		t.Run(want, func(t *testing.T) {
			if got := runOne(t, line).Body().Str(); got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
		})
	}
}

func TestKeptSamples_CarryProcessAndThreadIDs(t *testing.T) {
	got := runOne(t, lineInitPhase)

	if pid, tid := attrInt(got, "tightvnc.pid"), attrInt(got, "tightvnc.tid"); pid != 6020 || tid != 19112 {
		t.Errorf("pid/tid = %d/%d, want 6020/19112", pid, tid)
	}
}

func TestMarker_IsKeptAsAnAttribute(t *testing.T) {
	cases := map[string]string{lineIncoming: "+", lineInitPhase: "-", lineConnected: "!"}
	for line, want := range cases {
		if got := attrString(runOne(t, line), "tightvnc.marker"); got != want {
			t.Errorf("marker of %q = %q, want %q", line, got, want)
		}
	}
}

func TestMarker_OfAnUnmarkedLineIsEmpty(t *testing.T) {
	if got := attrString(runOne(t, lineAuthenticated), "tightvnc.marker"); got != "" {
		t.Errorf("marker = %q, want an empty value", got)
	}
}

// setHostZone makes Local mean the named zone for one test. The zone data comes from the
// time/tzdata import above, which only the test binary links.
func setHostZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load zone %s: %v", name, err)
	}
	saved := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = saved })
	return zone
}

func TestRecordTime_IsTheLogTimestampInUTCAndInTheHostZone(t *testing.T) {
	hostZone := setHostZone(t, "America/New_York")
	cases := map[string]time.Time{
		"UTC":   time.Date(2026, 11, 13, 20, 43, 6, 720_000_000, time.UTC),
		"Local": time.Date(2026, 11, 13, 20, 43, 6, 720_000_000, hostZone),
	}
	for zone, want := range cases {
		t.Run(zone, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.TimeZone = zone

			got := run(t, cfg, lineIncoming)

			if len(got) != 1 {
				t.Fatalf("kept %d records, want 1", len(got))
			}
			if !got[0].Timestamp().AsTime().Equal(want) {
				t.Errorf("record time = %v, want %v", got[0].Timestamp().AsTime(), want)
			}
		})
	}
}

func TestRecordTime_InTheFallBackHourOfTheHostZoneIsTheFirstOccurrence(t *testing.T) {
	setHostZone(t, "America/New_York")
	cfg := defaultConfig()

	got := run(t, cfg, "[ 1/ 1] 2026-11-01 01:30:00:000 + restart")

	want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	if len(got) != 1 {
		t.Fatalf("kept %d records, want 1", len(got))
	}
	if !got[0].Timestamp().AsTime().Equal(want) {
		t.Errorf("record time = %v, want the EDT instant %v", got[0].Timestamp().AsTime(), want)
	}
}

func TestRecordTime_InTheSpringForwardGapOfTheHostZoneIsTheObservedTime(t *testing.T) {
	setHostZone(t, "America/New_York")
	cfg := defaultConfig()

	got := run(t, cfg, "[ 1/ 1] 2026-03-08 02:30:00:000 + tick")

	if len(got) != 1 {
		t.Fatalf("kept %d records, want the line kept", len(got))
	}
	if !got[0].Timestamp().AsTime().Equal(observed) {
		t.Errorf("record time = %v, want the observed time %v", got[0].Timestamp().AsTime(), observed)
	}
}

func TestRecordTime_ThatNoZoneHasIsTheObservedTime(t *testing.T) {
	cases := map[string]string{
		"month 13": "[ 1/ 1] 2026-13-01 10:00:00:000 + tick",
		"hour 25":  "[ 1/ 1] 2026-11-13 25:00:00:000 + tick",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			got := run(t, utcConfig(), line)

			if len(got) != 1 {
				t.Fatalf("kept %d records, want the line kept", len(got))
			}
			if !got[0].Timestamp().AsTime().Equal(observed) {
				t.Errorf("record time = %v, want the observed time %v", got[0].Timestamp().AsTime(), observed)
			}
		})
	}
}

func TestEvent_ErrorAndDisconnectVocabulariesGiveDisconnect(t *testing.T) {
	got := runOne(t, stamped("   error while handling disconnect of client"))

	if v := attrString(got, "tightvnc.event"); v != "disconnect" {
		t.Errorf("tightvnc.event = %q, want disconnect", v)
	}
	if got.SeverityNumber() != plog.SeverityNumberError {
		t.Errorf("severity = %v, want ERROR: the order changes the event, not the severity", got.SeverityNumber())
	}
}

func TestSeverity_ComesFromTheMessageNotTheMarker(t *testing.T) {
	cases := map[string]plog.SeverityNumber{
		lineConnected:                               plog.SeverityNumberInfo,
		lineIdleTimeout:                             plog.SeverityNumberInfo,
		lineException:                               plog.SeverityNumberError,
		lineAuthenticated:                           plog.SeverityNumberInfo,
		stamped(" + Exception thrown"):              plog.SeverityNumberError,
		stamped("   An ERROR occurred"):             plog.SeverityNumberError,
		stamped("   write Failed"):                  plog.SeverityNumberError,
		stamped("   the connection was Rejected"):   plog.SeverityNumberWarn,
		stamped("   access denied"):                 plog.SeverityNumberWarn,
		stamped("   login REFUSED"):                 plog.SeverityNumberWarn,
		stamped("   low disk Warning"):              plog.SeverityNumberWarn,
		stamped("   authentication failed, denied"): plog.SeverityNumberError,
	}
	for line, want := range cases {
		if got := runOne(t, line).SeverityNumber(); got != want {
			t.Errorf("severity of %q = %v, want %v", line, got, want)
		}
	}
}

func TestSeverity_TextMatchesTheNumber(t *testing.T) {
	cases := map[string]string{
		lineConnected:                  "INFO",
		stamped("   access denied"):    "WARN",
		stamped(" ! Exception thrown"): "ERROR",
	}
	for line, want := range cases {
		if got := runOne(t, line).SeverityText(); got != want {
			t.Errorf("severity text of %q = %q, want %q", line, got, want)
		}
	}
}

func TestEvent_TypesEachKeptLine(t *testing.T) {
	cases := map[string]string{
		lineIncoming:                         "connect",
		lineConnected:                        "connect",
		lineAuthenticated:                    "auth",
		lineInitPhase:                        "lifecycle",
		lineIdleTimeout:                      "lifecycle",
		lineBuild:                            "lifecycle",
		lineInitWinSock:                      "lifecycle",
		lineStartMain:                        "lifecycle",
		lineServerStarted:                    "lifecycle",
		lineStopping:                         "lifecycle",
		lineException:                        "error",
		stamped(" ! Client #1 disconnected"): "disconnect",
		stamped("   Client #1 disconnect requested"):     "disconnect",
		stamped("   Authentication failed"):              "auth",
		stamped("   an error while handling disconnect"): "disconnect",
		stamped("   Something new happened"):             "other",
	}
	for line, want := range cases {
		if got := attrString(runOne(t, line), "tightvnc.event"); got != want {
			t.Errorf("event of %q = %q, want %q", line, got, want)
		}
	}
}

func TestAttributes_ConnectionLineGivesClientAddressAndServerPort(t *testing.T) {
	got := runOne(t, lineIncoming)

	if v := attrString(got, "client.address"); v != "192.0.2.10" {
		t.Errorf("client.address = %q, want 192.0.2.10", v)
	}
	if v := attrInt(got, "server.port"); v != 5900 {
		t.Errorf("server.port = %d, want 5900", v)
	}
}

func TestAttributes_ConnectionLineWithABadPortLeavesThePortOut(t *testing.T) {
	got := runOne(t, stamped(" + Incoming rfb connection from 192.0.2.10 to port abc"))

	if v := attrString(got, "client.address"); v != "192.0.2.10" {
		t.Errorf("client.address = %q, want 192.0.2.10", v)
	}
	if v := attrInt(got, "server.port"); v != -1 {
		t.Errorf("server.port = %d, want it absent", v)
	}
}

func TestAttributes_ClientLineGivesTheClientNumber(t *testing.T) {
	cases := map[string]int64{lineConnected: 0, stamped(" ! Client #12 connected"): 12}
	for line, want := range cases {
		if got := attrInt(runOne(t, line), "tightvnc.client.id"); got != want {
			t.Errorf("client id of %q = %d, want %d", line, got, want)
		}
	}
}

func TestAttributes_AuthLineGivesTheViewOnlyFlag(t *testing.T) {
	if got := attrInt(runOne(t, lineAuthenticated), "tightvnc.view_only"); got != 0 {
		t.Errorf("view_only = %d, want 0", got)
	}
	if got := attrInt(runOne(t, stamped("   Authenticated with view-only password = 1")), "tightvnc.view_only"); got != 1 {
		t.Errorf("view_only = %d, want 1", got)
	}
}

func TestAttributes_ServerStartLineGivesServerAddressAndPort(t *testing.T) {
	got := runOne(t, lineServerStarted)

	if v := attrString(got, "server.address"); v != "192.0.2.1" {
		t.Errorf("server.address = %q, want 192.0.2.1", v)
	}
	if v := attrInt(got, "server.port"); v != 5900 {
		t.Errorf("server.port = %d, want 5900", v)
	}
}

func TestBOM_IsRemovedFromTheFirstRecordOfANewFile(t *testing.T) {
	got := runOne(t, "\xef\xbb\xbf"+lineBuild)

	if want := "TightVNC Server Build on Jun  3 2026 at 14:25:03"; got.Body().Str() != want {
		t.Errorf("body = %q, want %q", got.Body().Str(), want)
	}
	if v := attrString(got, "tightvnc.event"); v != "lifecycle" {
		t.Errorf("event = %q, want the build line parsed and typed lifecycle", v)
	}
}

func TestBOM_IsRemovedFromALineOutsideTheGrammar(t *testing.T) {
	got := runOne(t, "\xef\xbb\xbf"+"not a log line")

	if got.Body().Str() != "not a log line" {
		t.Errorf("body = %q, want the BOM removed", got.Body().Str())
	}
	if v := attrString(got, "tightvnc.parse"); v != "failed" {
		t.Errorf("tightvnc.parse = %q, want failed", v)
	}
}

func TestBOM_AbsentLeavesTheBodyUnchanged(t *testing.T) {
	got := runOne(t, lineBuild)

	if want := "TightVNC Server Build on Jun  3 2026 at 14:25:03"; got.Body().Str() != want {
		t.Errorf("body = %q, want %q", got.Body().Str(), want)
	}
}

func TestUnparsed_LinesAreKeptRawAndMarkedFailed(t *testing.T) {
	cases := map[string]string{
		"outside the grammar": "this is not a TightVNC line",
		"truncated":           "[ 6020/ 6908] 2026-11-13 20:4",
		"no blank after time": "[ 6020/ 6908] 2026-11-13 20:43:06:720Incoming",
		"letters in the pid":  "[ 60x0/ 6908] 2026-11-13 20:43:06:720 + hello",
		"empty line":          "",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			got := runOne(t, line)

			if got.Body().Str() != line {
				t.Errorf("body = %q, want it untouched", got.Body().Str())
			}
			if v := attrString(got, "tightvnc.parse"); v != "failed" {
				t.Errorf("tightvnc.parse = %q, want failed", v)
			}
			if got.SeverityNumber() != plog.SeverityNumberUnspecified {
				t.Errorf("severity = %v, want it unset", got.SeverityNumber())
			}
		})
	}
}

func TestUnparsed_RecordFromAnotherSourceIsKeptUntouched(t *testing.T) {
	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Body().SetEmptyMap().PutStr("event", "login")

	got := process(t, utcConfig(), ld, processortest.NewNopSettings(component.MustNewType(typeStr)))

	if len(got) != 1 || got[0].Body().Type() != pcommon.ValueTypeMap {
		t.Fatalf("got %d records, want the map-bodied record kept as a map", len(got))
	}
	if v := attrString(got[0], "tightvnc.parse"); v != "failed" {
		t.Errorf("tightvnc.parse = %q, want failed", v)
	}
}

func TestParsedLine_HasNoParseAttribute(t *testing.T) {
	if v := attrString(runOne(t, lineIncoming), "tightvnc.parse"); v != "<absent>" {
		t.Errorf("tightvnc.parse = %q, want it absent on a parsed line", v)
	}
}

func TestKeepDrop_EveryKeepSampleIsKept(t *testing.T) {
	if got := run(t, utcConfig(), keepSamples...); len(got) != len(keepSamples) {
		t.Errorf("kept %d of %d keep samples", len(got), len(keepSamples))
	}
}

func TestKeepDrop_EveryNoiseSampleIsDropped(t *testing.T) {
	if got := run(t, utcConfig(), noiseSamples...); len(got) != 0 {
		t.Errorf("kept %d of %d noise samples, want 0", len(got), len(noiseSamples))
	}
}

func TestKeepDrop_UnmarkedLineWithNoListedPrefixIsKept(t *testing.T) {
	if got := run(t, utcConfig(), stamped(" : A brand new debug line")); len(got) != 1 {
		t.Errorf("kept %d records, want 1", len(got))
	}
}

func TestKeepDrop_MarkedLineWithANoisePrefixIsKept(t *testing.T) {
	if got := run(t, utcConfig(), stamped(" + Control client connected")); len(got) != 1 {
		t.Errorf("kept %d records, want 1: a marker keeps the line", len(got))
	}
}

func TestKeepDrop_EveryDefaultPrefixIsDropped(t *testing.T) {
	for _, prefix := range defaultConfig().DropPrefixes {
		t.Run(prefix, func(t *testing.T) {
			if got := run(t, utcConfig(), stamped(" : "+prefix+" 42")); len(got) != 0 {
				t.Errorf("kept a line starting with %q", prefix)
			}
		})
	}
}

func TestKeepDrop_ConfiguredPrefixesReplaceTheDefaults(t *testing.T) {
	cfg := utcConfig()
	cfg.DropPrefixes = []string{"Only this"}

	got := run(t, cfg, stamped(" : Control client sends process ID"), stamped(" : Only this one"))

	if len(got) != 1 || got[0].Body().Str() != "Control client sends process ID" {
		t.Errorf("kept %d records, want only the default-prefix line kept", len(got))
	}
}

func TestKeepDrop_NoisePrefixLineWithAnErrorWordIsKeptAsError(t *testing.T) {
	cases := []string{
		stamped(" : Control client request failed"),
		stamped(" : Control client hit an ERROR"),
		stamped("   process memory usage error"),
	}
	for _, line := range cases {
		got := run(t, utcConfig(), line)

		if len(got) != 1 {
			t.Fatalf("kept %d records for %q, want 1", len(got), line)
		}
		if got[0].SeverityNumber() != plog.SeverityNumberError {
			t.Errorf("severity of %q = %v, want ERROR", line, got[0].SeverityNumber())
		}
	}
}

func TestDroppedBatch_IsNotForwarded(t *testing.T) {
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, utcConfig(), sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}

	if err := proc.ConsumeLogs(context.Background(), newLogs(noiseSamples...)); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if got := len(sink.AllLogs()); got != 0 {
		t.Errorf("next consumer saw %d batches, want none", got)
	}
}

func TestMixedBatch_OnlyTheNoiseResourceDisappears(t *testing.T) {
	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	proc, err := NewFactory().CreateLogs(context.Background(), set, utcConfig(), sink)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	ld := newLogs(noiseSamples...)
	newLogs(lineIncoming).ResourceLogs().MoveAndAppendTo(ld.ResourceLogs())

	if err := proc.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if got := sink.LogRecordCount(); got != 1 {
		t.Errorf("next consumer saw %d records, want 1", got)
	}
	if got := sink.AllLogs()[0].ResourceLogs().Len(); got != 1 {
		t.Errorf("resources = %d, want the all-noise resource removed", got)
	}
}

func countersByName(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				got[m.Name] += dp.Value
			}
		}
	}
	return got
}

func TestCounters_CountEveryRecordOnce(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(component.MustNewType(typeStr))
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	bodies := []string{
		lineIncoming, lineAuthenticated,
		noiseSamples[0], noiseSamples[1], noiseSamples[2],
		"not a log line", "",
	}

	process(t, utcConfig(), newLogs(bodies...), set)

	want := map[string]int64{
		"otelcol_tightvnc_records_kept":     2,
		"otelcol_tightvnc_records_dropped":  3,
		"otelcol_tightvnc_records_unparsed": 2,
	}
	got := countersByName(t, reader)
	if !maps.Equal(got, want) {
		t.Errorf("counters = %v, want %v", got, want)
	}
	if sum := got["otelcol_tightvnc_records_kept"] + got["otelcol_tightvnc_records_dropped"] +
		got["otelcol_tightvnc_records_unparsed"]; sum != int64(len(bodies)) {
		t.Errorf("counters sum to %d, want the %d lines read", sum, len(bodies))
	}
}
