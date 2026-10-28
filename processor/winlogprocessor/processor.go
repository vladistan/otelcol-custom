// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

// noopProcessor passes data through unchanged until the processor is built.
type noopProcessor struct {
	next consumer.Logs
}

func (p *noopProcessor) Start(context.Context, component.Host) error { return nil }

func (p *noopProcessor) Shutdown(context.Context) error { return nil }

func (p *noopProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (p *noopProcessor) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	return p.next.ConsumeLogs(ctx, logs)
}
