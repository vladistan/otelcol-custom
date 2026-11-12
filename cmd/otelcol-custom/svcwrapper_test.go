// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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
