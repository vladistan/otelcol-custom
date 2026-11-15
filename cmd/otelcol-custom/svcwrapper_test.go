// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore sppsvc kdumpctl statm lsass

package main

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type fakeTransport struct {
	mu           sync.Mutex
	events       []*sentry.Event
	flushTimeout time.Duration
	flushes      int
}

func (f *fakeTransport) Configure(sentry.ClientOptions) {}

func (f *fakeTransport) SendEvent(event *sentry.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeTransport) Flush(timeout time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushes++
	f.flushTimeout = timeout

	return true
}

func (f *fakeTransport) eventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.events)
}

func (f *fakeTransport) flushCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.flushes
}

func (f *fakeTransport) lastFlushTimeout() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.flushTimeout
}

func (f *fakeTransport) firstEvent() *sentry.Event {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.events[0]
}

func newFakeHub(t *testing.T) (*sentry.Hub, *fakeTransport) {
	t.Helper()
	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://public@example.invalid/1",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return sentry.NewHub(client, sentry.NewScope()), transport
}

func TestRunReportingSendsOneEventForANonZeroCode(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, time.Second, func() (bool, uint32) { return false, 1067 })

	if got := transport.eventCount(); got != 1 {
		t.Fatalf("got %d events, want 1", got)
	}
}

func TestRunReportingTagsTheEventWithTheExitCode(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, time.Second, func() (bool, uint32) { return false, 1067 })

	if got := transport.firstEvent().Tags["exit_code"]; got != "1067" {
		t.Errorf("exit_code tag = %q, want 1067", got)
	}
}

func TestRunReportingFlushesOnce(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, time.Second, func() (bool, uint32) { return true, 2 })

	if got := transport.flushCount(); got != 1 {
		t.Errorf("flushes = %d, want 1", got)
	}
}

func TestRunReportingFlushesWithTheConfiguredTimeout(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, 3*time.Second, func() (bool, uint32) { return true, 2 })

	if got := transport.lastFlushTimeout(); got != 3*time.Second {
		t.Errorf("flush timeout = %v, want 3s", got)
	}
}

func TestRunReportingReturnsTheSameCode(t *testing.T) {
	hub, _ := newFakeHub(t)
	_, code := runReporting(hub, time.Second, func() (bool, uint32) { return true, 1067 })

	if code != 1067 {
		t.Errorf("code = %d, want 1067", code)
	}
}

func TestRunReportingReturnsTheServiceSpecificFlag(t *testing.T) {
	hub, _ := newFakeHub(t)
	specific, _ := runReporting(hub, time.Second, func() (bool, uint32) { return true, 1067 })

	if !specific {
		t.Error("service-specific flag was dropped")
	}
}

func TestRunReportingReturnsZeroOnSuccess(t *testing.T) {
	hub, _ := newFakeHub(t)
	_, code := runReporting(hub, time.Second, func() (bool, uint32) { return false, 0 })

	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

func TestRunReportingSendsNoEventOnSuccess(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, time.Second, func() (bool, uint32) { return false, 0 })

	if got := transport.eventCount(); got != 0 {
		t.Errorf("events = %d, want 0", got)
	}
}

func TestRunReportingDoesNotFlushOnSuccess(t *testing.T) {
	hub, transport := newFakeHub(t)
	runReporting(hub, time.Second, func() (bool, uint32) { return false, 0 })

	if got := transport.flushCount(); got != 0 {
		t.Errorf("flushes = %d, want 0", got)
	}
}

func TestSentryFlushTimeoutDefaultsToFiveSeconds(t *testing.T) {
	t.Setenv(sentryFlushTimeoutEnv, "")

	if got := sentryFlushTimeout(); got != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", got)
	}
}

func TestSentryFlushTimeoutReadsTheEnvironment(t *testing.T) {
	t.Setenv(sentryFlushTimeoutEnv, "1500ms")

	if got := sentryFlushTimeout(); got != 1500*time.Millisecond {
		t.Errorf("timeout = %v, want 1.5s", got)
	}
}

func TestSentryFlushTimeoutRejectsBadValues(t *testing.T) {
	for _, bad := range []string{"soon", "-2s", "0s"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(sentryFlushTimeoutEnv, bad)

			if got := sentryFlushTimeout(); got != defaultSentryFlushTimeout {
				t.Errorf("got %v, want the 5s default", got)
			}
		})
	}
}

const (
	testDefaultDSN  = "https://default@example.invalid/9"
	testOverrideDSN = "https://override@example.invalid/7"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	previous := log.Writer()

	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	return &buf
}

func TestSentryDSNUsesTheDefaultWhenUnset(t *testing.T) {
	t.Setenv(sentryDSNEnv, "")

	if got := sentryDSN(testDefaultDSN); got != testDefaultDSN {
		t.Errorf("dsn = %q, want the default", got)
	}
}

func TestSentryDSNUsesTheOverrideWhenValid(t *testing.T) {
	t.Setenv(sentryDSNEnv, testOverrideDSN)

	if got := sentryDSN(testDefaultDSN); got != testOverrideDSN {
		t.Errorf("dsn = %q, want the override", got)
	}
}

func TestSentryDSNFallsBackOnInvalidValues(t *testing.T) {
	for _, bad := range []string{"not-a-dsn", "   ", "https://example.invalid/1", "ftp://k@example.invalid/1"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(sentryDSNEnv, bad)
			captureLog(t)

			if got := sentryDSN(testDefaultDSN); got != testDefaultDSN {
				t.Errorf("dsn = %q, want the default", got)
			}
		})
	}
}

func TestSentryDSNWarnsOnInvalidValuesWithoutPrintingThem(t *testing.T) {
	const secret = "bogus-dsn-secret-marker" // pragma: allowlist secret

	t.Setenv(sentryDSNEnv, secret)
	buf := captureLog(t)
	sentryDSN(testDefaultDSN)

	out := buf.String()
	if !strings.Contains(out, sentryDSNEnv) {
		t.Errorf("log %q does not name %s", out, sentryDSNEnv)
	}

	if strings.Contains(out, secret) {
		t.Errorf("log %q leaks the value", out)
	}
}

func TestSentryDSNLogsNothingForAValidOverride(t *testing.T) {
	t.Setenv(sentryDSNEnv, testOverrideDSN)
	buf := captureLog(t)
	sentryDSN(testDefaultDSN)

	if buf.Len() != 0 {
		t.Errorf("unexpected log output %q", buf.String())
	}
}

func TestSentryDSNWiringInClientOptionsUsesTheOverride(t *testing.T) {
	t.Setenv(sentryDSNEnv, testOverrideDSN)

	if got := sentryClientOptions("test").Dsn; got != testOverrideDSN {
		t.Errorf("client options dsn = %q, want the override", got)
	}
}

func TestSentryDSNWiringInClientOptionsKeepsTheBuiltInDefault(t *testing.T) {
	t.Setenv(sentryDSNEnv, "")

	if got := sentryClientOptions("test").Dsn; got != SentryDSN {
		t.Errorf("client options dsn = %q, want the built-in DSN", got)
	}
}

func TestSentryDSNWiringInClientOptionsFallsBackWithoutLoggingTheValue(t *testing.T) {
	const bad = "bogus-wiring-marker" // pragma: allowlist secret

	t.Setenv(sentryDSNEnv, bad)
	buf := captureLog(t)

	if got := sentryClientOptions("test").Dsn; got != SentryDSN {
		t.Errorf("client options dsn = %q, want the built-in DSN", got)
	}

	out := buf.String()
	if !strings.Contains(out, sentryDSNEnv) || strings.Contains(out, bad) {
		t.Errorf("log %q must name %s and not print the value", out, sentryDSNEnv)
	}
}

func TestSentryDSNWiringInClientOptionsKeepsEnvironmentAndRelease(t *testing.T) {
	t.Setenv(sentryDSNEnv, "")

	options := sentryClientOptions("staging")
	if options.Environment != "staging" || options.Release != serviceName+"@"+version {
		t.Errorf("environment %q release %q", options.Environment, options.Release)
	}
}

// bindFakeClient points the global Sentry hub at a fake transport for one test.
func bindFakeClient(t *testing.T) *fakeTransport {
	t.Helper()

	transport := &fakeTransport{}

	client, err := sentry.NewClient(sentry.ClientOptions{Dsn: testDefaultDSN, Transport: transport})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	hub := sentry.CurrentHub()
	previous := hub.Client()

	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(previous) })

	return transport
}

// newSentryTestLogger builds a logger with the production Sentry options over
// an in-memory core.
func newSentryTestLogger() *zap.Logger {
	core, _ := observer.New(zapcore.DebugLevel)

	options := append([]zap.Option{zap.AddCaller()}, createSentryLoggingOptions()...)

	return zap.New(core, options...)
}

func TestSentryNoiseFilterDropsKnownMessages(t *testing.T) {
	messages := []string{
		"context canceled",
		"connection reset by peer",
		"failed to upload metrics: Post \"https://gw.example.invalid/v1/metrics\": write tcp 10.0.0.2:5000->10.0.0.3:4318: use of closed network connection",
		"USE OF CLOSED NETWORK CONNECTION",
		"EvtFormatMessage failed: failed to find MUI file for provider",
		"Failed To Find MUI File",
	}
	for _, msg := range messages {
		t.Run(msg, func(t *testing.T) {
			if !filterNoiseErrors(msg) {
				t.Errorf("%q should be filtered", msg)
			}
		})
	}
}

func TestSentryNoiseFilterKeepsRealErrors(t *testing.T) {
	for _, msg := range []string{"exporter failed", "failed to upload metrics: status 500", "closed"} {
		t.Run(msg, func(t *testing.T) {
			if filterNoiseErrors(msg) {
				t.Errorf("%q should reach Sentry", msg)
			}
		})
	}
}

func TestSentryCaptureSendsOneEventPerError(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Error("exporter failed")

	if got := transport.eventCount(); got != 1 {
		t.Fatalf("got %d events, want 1", got)
	}
}

func TestSentryCaptureSkipsNoiseErrors(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Error("failed to upload metrics: use of closed network connection")

	if got := transport.eventCount(); got != 0 {
		t.Errorf("got %d events, want 0", got)
	}
}

const (
	muiErrorText    = "render deep event: syscall to 'EvtFormatMessage' failed: The resource loader failed to find MUI file."
	closedErrorText = "Post \"https://gw.example.invalid/v1/metrics\": write tcp 10.0.0.2:5000->10.0.0.3:4318: use of closed network connection"
)

func TestSentryCaptureSkipsNoiseInTheErrorField(t *testing.T) {
	cases := map[string]zap.Field{
		"mui error":        zap.Error(errors.New(muiErrorText)),
		"closed error":     zap.Error(errors.New(closedErrorText)),
		"upper-case error": zap.Error(errors.New("USE OF CLOSED NETWORK CONNECTION")),
		"string error key": zap.String("error", muiErrorText),
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error("process event", field)

			if got := transport.eventCount(); got != 0 {
				t.Errorf("got %d events, want 0", got)
			}
		})
	}
}

func TestSentryCaptureSkipsNoiseInTheErrorFieldOfAChildLogger(t *testing.T) {
	transport := bindFakeClient(t)
	child := newSentryTestLogger().With(zap.Error(errors.New(muiErrorText)))
	child.Error("process event", zap.String("operator_type", "winlog"))

	if got := transport.eventCount(); got != 0 {
		t.Errorf("got %d events, want 0", got)
	}
}

func TestSentryCaptureKeepsOrdinaryErrorFields(t *testing.T) {
	cases := map[string]zap.Field{
		"error type":       zap.Error(errors.New("render deep event: access denied")),
		"string error key": zap.String("error", "access denied"),
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error("process event", field)

			if got := transport.eventCount(); got != 1 {
				t.Errorf("got %d events, want 1", got)
			}
		})
	}
}

func TestSentryCaptureIgnoresNoiseInOtherFields(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Error("process event", zap.String("detail", muiErrorText))

	if got := transport.eventCount(); got != 1 {
		t.Errorf("got %d events, want 1", got)
	}
}

func TestSentryCaptureIgnoresANilErrorField(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Error("process event", zap.Error(nil))

	if got := transport.eventCount(); got != 1 {
		t.Errorf("got %d events, want 1", got)
	}
}

const testSelfTelemetryMessage = "OpenTelemetry internal telemetry error"

func TestSentryCaptureSkipsUploadConnectionFailures(t *testing.T) {
	cases := []struct{ name, message, errText string }{
		{"self-metrics EOF", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": EOF`},
		{"self-metrics read tcp reset", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": read tcp 10.0.0.2:5000->10.0.0.3:4318: read: connection reset by peer`},
		{"self-metrics closed connection", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": write tcp 10.0.0.2:5000->10.0.0.3:4318: use of closed network connection`},
		{"lower-case eof", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": eof`},
		{"upload logs under another message", "exporter failed", `failed to upload logs: Post "http://gw.example.invalid:4318/v1/logs": EOF`},
		{"upload traces under another message", "exporter failed", `failed to upload traces: Post "http://gw.example.invalid:4318/v1/traces": EOF`},
		{"self-telemetry message, other upload text", testSelfTelemetryMessage, `Post "http://gw.example.invalid:4318/v1/metrics": EOF`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error(tc.message, zap.Error(errors.New(tc.errText)))

			if got := transport.eventCount(); got != 0 {
				t.Errorf("got %d events, want 0", got)
			}
		})
	}
}

func TestSentryCaptureKeepsOtherUploadFailures(t *testing.T) {
	cases := []struct{ name, message, errText string }{
		{"503 response", testSelfTelemetryMessage, "failed to upload metrics: unexpected HTTP status 503 Service Unavailable"},
		{"DNS failure", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": dial tcp: lookup gw.example.invalid: no such host`},
		{"read tcp i/o timeout", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": read tcp 10.0.0.2:5000->10.0.0.3:4318: i/o timeout`},
		{"read tcp timeout under an upload message", "exporter failed", `failed to upload traces: Post "http://gw.example.invalid:4318/v1/traces": read tcp 10.0.0.2:5000->10.0.0.3:4318: i/o timeout`},
		{"read tcp forcibly closed", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": read tcp 10.0.0.2:5000->10.0.0.3:4318: An existing connection was forcibly closed by the remote host.`},
		{"context deadline exceeded", testSelfTelemetryMessage, `failed to upload metrics: Post "http://gw.example.invalid:4318/v1/metrics": context deadline exceeded`},
		{"EOF inside the text", testSelfTelemetryMessage, "failed to upload metrics: EOF while reading the body: status 500"},
		{"EOF outside an upload", "process event", "read body: EOF"},
		{"read tcp outside an upload", "process event", "read tcp 10.0.0.2:5000->10.0.0.3:4318: i/o timeout"},
		{"upload text with another message and no connection error", "exporter failed", "failed to upload metrics: status 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error(tc.message, zap.Error(errors.New(tc.errText)))

			if got := transport.eventCount(); got != 1 {
				t.Errorf("got %d events, want 1", got)
			}
		})
	}
}

const scrapeErrorMessage = "Error scraping metrics"

func TestSentryCaptureSkipsProcessExitRaceErrors(t *testing.T) {
	cases := map[string]string{
		"all three reads":       `error reading cpu times for process "sppsvc.exe" (pid 7320): The parameter is incorrect.; error reading memory info for process "sppsvc.exe" (pid 7320): The parameter is incorrect.; error reading handle count for process "sppsvc.exe" (pid 7320): The parameter is incorrect.`,
		"one read":              `error reading cpu times for process "conhost.exe" (pid 4100): The parameter is incorrect.`,
		"two processes":         `error reading cpu times for process "WmiPrvSE.exe" (pid 7432): The parameter is incorrect.; error reading cpu times for process "conhost.exe" (pid 4100): The parameter is incorrect.`,
		"upper-case text":       `ERROR READING CPU TIMES FOR PROCESS "SPPSVC.EXE" (PID 7320): THE PARAMETER IS INCORRECT.`,
		"linux file vanished":   `error reading cpu times for process "kdumpctl" (pid 733): open /proc/733/stat: no such file or directory; error reading memory utilization for process "kdumpctl" (pid 733): open /proc/733/statm: no such file or directory`,
		"no trailing full stop": `error reading cpu times for process "sppsvc.exe" (pid 7320): The parameter is incorrect`,
	}
	for name, errText := range cases {
		t.Run(name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error(scrapeErrorMessage, zap.Error(errors.New(errText)))

			if got := transport.eventCount(); got != 0 {
				t.Errorf("got %d events, want 0", got)
			}
		})
	}
}

func TestSentryCaptureKeepsOtherProcessReadErrors(t *testing.T) {
	cases := map[string]string{
		"access denied":                 `error reading cpu times for process "lsass.exe" (pid 780): Access is denied.`,
		"one denied among races":        `error reading cpu times for process "sppsvc.exe" (pid 7320): The parameter is incorrect.; error reading memory info for process "lsass.exe" (pid 780): Access is denied.`,
		"race text without a process":   `error reading cpu times for host: The parameter is incorrect.`,
		"race text from another source": `The parameter is incorrect.`,
		"extra text after a race":       `error reading cpu times for process "sppsvc.exe" (pid 7320): The parameter is incorrect.; scrape aborted`,
		"other wording":                 `error reading cpu times for process "sppsvc.exe" (pid 7320): The handle is invalid.`,
	}
	for name, errText := range cases {
		t.Run(name, func(t *testing.T) {
			transport := bindFakeClient(t)
			newSentryTestLogger().Error(scrapeErrorMessage, zap.Error(errors.New(errText)))

			if got := transport.eventCount(); got != 1 {
				t.Errorf("got %d events, want 1", got)
			}
		})
	}
}

func TestSentryCaptureIgnoresLevelsBelowError(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Warn("exporter slow")

	if got := transport.eventCount(); got != 0 {
		t.Errorf("got %d events, want 0", got)
	}
}

func TestSentryCaptureFingerprintsByMessageAndCaller(t *testing.T) {
	transport := bindFakeClient(t)
	newSentryTestLogger().Error("exporter failed")

	fingerprint := transport.firstEvent().Fingerprint
	if len(fingerprint) != 2 || fingerprint[0] != "exporter failed" {
		t.Fatalf("fingerprint = %q, want [message, caller file]", fingerprint)
	}

	if !strings.HasSuffix(fingerprint[1], "svcwrapper_test.go") {
		t.Errorf("caller file = %q, want svcwrapper_test.go", fingerprint[1])
	}
}

func TestSentryStartupEventUsesAFixedFingerprint(t *testing.T) {
	for _, host := range []string{"HOST-A", "HOST-B"} {
		got := startupEvent(host).Fingerprint
		if len(got) != 1 || got[0] != "service-started" {
			t.Errorf("host %s: fingerprint = %q, want [service-started]", host, got)
		}
	}
}

func TestSentryStartupEventTagsTheHost(t *testing.T) {
	if got := startupEvent("HOST-A").Tags["server_name"]; got != "HOST-A" {
		t.Errorf("server_name tag = %q, want HOST-A", got)
	}
}

func TestSentryStartupEventNamesTheServiceAndHost(t *testing.T) {
	got := startupEvent("HOST-A").Message
	if !strings.Contains(got, serviceName) || !strings.Contains(got, "HOST-A") {
		t.Errorf("message = %q, want service and host", got)
	}
}

func TestSentryStartupEventIsInfoLevel(t *testing.T) {
	if got := startupEvent("HOST-A").Level; got != sentry.LevelInfo {
		t.Errorf("level = %q, want info", got)
	}
}
