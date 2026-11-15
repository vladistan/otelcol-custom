// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// optionalReceiverTypes are the receivers whose failed Start is logged and skipped, so the collector keeps
// running. Every other receiver, journald and host_metrics included, stays fail-hard: its failure stops the
// collector. The list is code, not config, so a deployment cannot make a core receiver optional by accident.
var optionalReceiverTypes = []string{"docker_stats", "tcp_check"}

// withOptionalReceivers wraps a factories function so the listed receivers do not stop the collector when
// they fail to start.
func withOptionalReceivers(build func() (otelcol.Factories, error), types ...string) func() (otelcol.Factories, error) {
	if len(types) == 0 {
		types = optionalReceiverTypes
	}
	return func() (otelcol.Factories, error) {
		factories, err := build()
		if err != nil {
			return factories, err
		}
		return wrapOptionalReceivers(factories, types...), nil
	}
}

// wrapOptionalReceivers replaces the factory of each listed receiver type that the build has. A type the build
// does not have is ignored.
func wrapOptionalReceivers(factories otelcol.Factories, types ...string) otelcol.Factories {
	for _, name := range types {
		typ, err := component.NewType(name)
		if err != nil {
			continue
		}
		inner, ok := factories.Receivers[typ]
		if !ok {
			continue
		}
		factories.Receivers[typ] = newOptionalFactory(inner)
	}
	return factories
}

// newOptionalFactory returns a factory of the same type, default config, signals and stability as inner whose
// receivers swallow a failed Start.
func newOptionalFactory(inner receiver.Factory) receiver.Factory {
	var options []receiver.FactoryOption
	if inner.MetricsStability() != component.StabilityLevelUndefined {
		options = append(options, receiver.WithMetrics(
			func(ctx context.Context, set receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
				created, err := inner.CreateMetrics(ctx, set, cfg, next)
				if err != nil {
					return nil, err
				}
				return newOptionalReceiver(created, set), nil
			}, inner.MetricsStability()))
	}
	if inner.LogsStability() != component.StabilityLevelUndefined {
		options = append(options, receiver.WithLogs(
			func(ctx context.Context, set receiver.Settings, cfg component.Config, next consumer.Logs) (receiver.Logs, error) {
				created, err := inner.CreateLogs(ctx, set, cfg, next)
				if err != nil {
					return nil, err
				}
				return newOptionalReceiver(created, set), nil
			}, inner.LogsStability()))
	}
	if inner.TracesStability() != component.StabilityLevelUndefined {
		options = append(options, receiver.WithTraces(
			func(ctx context.Context, set receiver.Settings, cfg component.Config, next consumer.Traces) (receiver.Traces, error) {
				created, err := inner.CreateTraces(ctx, set, cfg, next)
				if err != nil {
					return nil, err
				}
				return newOptionalReceiver(created, set), nil
			}, inner.TracesStability()))
	}
	return receiver.NewFactory(inner.Type(), inner.CreateDefaultConfig, options...)
}

// Retry timing for a receiver that failed to start. Variables so tests can shorten them.
var (
	retryInitialDelay = 30 * time.Second
	retryMaxDelay     = 5 * time.Minute
)

// warnEveryFailure logs a WARN on the first failed start and then on every tenth.
const warnEveryFailure = 10

// credentialsInURL matches the user:password part of a URL in error text.
var credentialsInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)

// redact hides URL credentials in text that is logged or reported as a status.
func redact(text string) string {
	return credentialsInURL.ReplaceAllString(text, "${1}***@")
}

func nextRetryDelay(current time.Duration) time.Duration {
	return min(current*2, retryMaxDelay)
}

// optionalReceiver runs the receiver it wraps. A Start that fails is logged, counted and answered with nil.
// A background goroutine then retries the real Start with a doubling delay until it succeeds or Shutdown runs.
type optionalReceiver struct {
	component.Component
	id      string
	logger  *zap.Logger
	skipped metric.Int64Counter // nil when the meter cannot create the counter
	attrs   metric.MeasurementOption

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// newOptionalReceiver wraps created. The skipped counter is best effort: without it the warning is still
// logged. The counter is added at zero so it exists before the first skip.
func newOptionalReceiver(created component.Component, set receiver.Settings) *optionalReceiver {
	id := set.ID.String()
	r := &optionalReceiver{
		Component: created,
		id:        id,
		logger:    set.Logger,
		attrs:     metric.WithAttributes(attribute.String("receiver", id)),
	}
	counter, err := set.MeterProvider.Meter("github.com/vladistan/otelcol-custom/cmd/otelcol-custom").Int64Counter(
		"otelcol_optional_receiver_skipped",
		metric.WithDescription("Optional receivers whose start failed and that were skipped."),
		metric.WithUnit("{receiver}"),
	)
	if err != nil {
		set.Logger.Debug("cannot create the optional receiver counter", zap.Error(err))
		return r
	}
	counter.Add(context.Background(), 0, r.attrs)
	r.skipped = counter
	return r
}

func (r *optionalReceiver) Start(ctx context.Context, host component.Host) error {
	err := r.Component.Start(ctx, host)
	if err == nil {
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		return nil
	}
	r.logger.Warn("optional receiver did not start and is skipped; the collector keeps running and retries the start",
		zap.String("receiver", r.id), zap.String("error", redact(err.Error())))
	if r.skipped != nil {
		r.skipped.Add(ctx, 1, r.attrs)
	}
	retryCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.mu.Lock()
	r.cancel = cancel
	r.done = make(chan struct{})
	done := r.done
	r.mu.Unlock()
	go r.retry(retryCtx, host, done)
	return nil
}

// retry starts the wrapped receiver again until it succeeds or ctx ends. The first status report comes after
// the first delay, so it follows the OK the collector reports when Start returns.
func (r *optionalReceiver) retry(ctx context.Context, host component.Host, done chan struct{}) {
	defer close(done)
	delay := retryInitialDelay
	for failures := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		err := r.Component.Start(ctx, host)
		if ctx.Err() != nil && err != nil {
			return
		}
		if err == nil {
			r.mu.Lock()
			r.started = true
			r.mu.Unlock()
			r.logger.Info("optional receiver started after earlier failures",
				zap.String("receiver", r.id), zap.Int("failed_starts", failures))
			componentstatus.ReportStatus(host, componentstatus.NewEvent(componentstatus.StatusOK))
			return
		}
		failures++
		text := redact(err.Error())
		if failures%warnEveryFailure == 0 {
			r.logger.Warn("optional receiver still has not started; retrying",
				zap.String("receiver", r.id), zap.Int("failed_starts", failures), zap.String("error", text))
		}
		componentstatus.ReportStatus(host, componentstatus.NewRecoverableErrorEvent(errors.New(text)))
		delay = nextRetryDelay(delay)
	}
}

// Shutdown stops the retry and waits for it. The wrapped receiver is shut down only if it started.
func (r *optionalReceiver) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		return nil
	}
	return r.Component.Shutdown(ctx)
}
