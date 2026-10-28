// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
)

// noopReceiver starts and stops without reading anything until the receiver is built.
type noopReceiver struct{}

func (r *noopReceiver) Start(context.Context, component.Host) error { return nil }

func (r *noopReceiver) Shutdown(context.Context) error { return nil }
