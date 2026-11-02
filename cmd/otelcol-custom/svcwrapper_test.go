// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
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
