// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/getsentry/sentry-go"
)

const (
	defaultSentryFlushTimeout = 5 * time.Second
	sentryFlushTimeoutEnv     = "OTELCOL_CUSTOM_SENTRY_FLUSH_TIMEOUT"
	sentryDSNEnv              = "OTELCOL_CUSTOM_SENTRY_DSN"
)

// sentryDSN returns the DSN from the environment when it parses, and fallback
// when the variable is unset, empty or invalid. The value is never logged.
func sentryDSN(fallback string) string {
	raw := os.Getenv(sentryDSNEnv)
	if raw == "" {
		return fallback
	}

	if _, err := sentry.NewDsn(raw); err != nil {
		log.Printf("WARNING: ignoring %s: not a valid DSN, using the built-in default", sentryDSNEnv)

		return fallback
	}

	return raw
}

// sentryFlushTimeout reads the flush timeout from the environment and falls
// back to 5s when it is unset, unparsable or not positive.
func sentryFlushTimeout() time.Duration {
	raw := os.Getenv(sentryFlushTimeoutEnv)
	if raw == "" {
		return defaultSentryFlushTimeout
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		log.Printf("WARNING: ignoring %s=%q, using %s", sentryFlushTimeoutEnv, raw, defaultSentryFlushTimeout)

		return defaultSentryFlushTimeout
	}

	return timeout
}

// reportExitCode sends one event for a non-zero exit code, then flushes.
func reportExitCode(hub *sentry.Hub, code uint32, timeout time.Duration) {
	if code == 0 {
		return
	}

	codeText := strconv.FormatUint(uint64(code), 10)
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = "collector service exited with code " + codeText
	event.Tags["exit_code"] = codeText
	event.Extra["exit_code"] = code
	event.Fingerprint = []string{"service-exit-code", codeText}
	hub.CaptureEvent(event)
	hub.Flush(timeout)
}

// runReporting runs execute and reports a non-zero code to Sentry. The code
// goes back to the caller unchanged.
func runReporting(hub *sentry.Hub, timeout time.Duration, execute func() (bool, uint32)) (bool, uint32) {
	specific, code := execute()
	reportExitCode(hub, code, timeout)

	return specific, code
}
