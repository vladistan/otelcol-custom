// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"

	"go.opentelemetry.io/collector/otelcol"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func init() {
	if err := registerRotatingSink(); err != nil {
		log.Fatalf("register rotating log sink: %v", err)
	}
}

func run(params otelcol.CollectorSettings) error {
	// No service name is needed when the Service Control Manager starts us.
	if err := svc.Run("", otelcol.NewSvcHandler(params)); err != nil {
		if errors.Is(err, windows.ERROR_FAILED_SERVICE_CONTROLLER_CONNECT) {
			// Not running as a service: run interactively.
			return runInteractive(params)
		}

		return fmt.Errorf("failed to start collector server: %w", err)
	}

	return nil
}
