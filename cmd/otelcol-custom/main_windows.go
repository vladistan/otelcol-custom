// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/getsentry/sentry-go"
	"go.opentelemetry.io/collector/otelcol"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func init() {
	if err := registerRotatingSink(); err != nil {
		log.Fatalf("register rotating log sink: %v", err)
	}
}

// sentryHandler reports a non-zero service exit code to Sentry.
type sentryHandler struct {
	inner svc.Handler
}

func (h sentryHandler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	return runReporting(sentry.CurrentHub(), sentryFlushTimeout(), func() (bool, uint32) {
		return h.inner.Execute(args, requests, status)
	})
}

func run(params otelcol.CollectorSettings) error {
	// No service name is needed when the Service Control Manager starts us.
	if err := svc.Run("", sentryHandler{inner: otelcol.NewSvcHandler(params)}); err != nil {
		if errors.Is(err, windows.ERROR_FAILED_SERVICE_CONTROLLER_CONNECT) {
			// Not running as a service: run interactively.
			return runInteractive(params)
		}

		return fmt.Errorf("failed to start collector server: %w", err)
	}

	return nil
}
