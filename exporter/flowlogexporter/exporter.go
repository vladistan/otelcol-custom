// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// maxUnknownAddresses bounds the set of exporter addresses that have had their one warning.
const maxUnknownAddresses = 4096

var errNotConnected = errors.New("not connected to ClickHouse yet")

// logsExporter writes the records of a netflow pipeline to the flow table.
type logsExporter struct {
	cfg  *Config
	log  *zap.Logger
	tel  *telemetry
	m    *mapper
	dial func(context.Context) (conn, error)

	mu     sync.RWMutex
	c      conn
	cancel context.CancelFunc
	wg     sync.WaitGroup

	warnedSchema atomic.Bool
	unknownMu    sync.Mutex
	unknownSeen  map[string]struct{}
}

func newLogsExporter(cfg *Config, set exporter.Settings, collector string, dial func(context.Context) (conn, error)) (*logsExporter, error) {
	tel, err := newTelemetry(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	e := &logsExporter{cfg: cfg, log: set.Logger, tel: tel, dial: dial, unknownSeen: map[string]struct{}{}}
	e.m = newMapper(cfg, collector,
		func(reason string) { tel.mappingWarning(context.Background(), reason) },
		e.noteUnknownExporter)
	return e, nil
}

// noteUnknownExporter counts a record from an address that is not in routers and warns once per address.
func (e *logsExporter) noteUnknownExporter(addr string) {
	e.tel.unknownExporter.Add(context.Background(), 1)
	e.unknownMu.Lock()
	defer e.unknownMu.Unlock()
	if _, seen := e.unknownSeen[addr]; seen || len(e.unknownSeen) >= maxUnknownAddresses {
		return
	}
	e.unknownSeen[addr] = struct{}{}
	e.log.Warn("flow records from an exporter address that is not in routers; account, region and direction stay empty",
		zap.String("exporter", addr))
}

// start returns at once. It warns about an empty password and connects in the background, so a
// ClickHouse that is down at start does not stop the collector.
func (e *logsExporter) start(ctx context.Context, _ component.Host) error {
	if e.cfg.Password == "" {
		if v := e.cfg.PasswordVariable; v != "" {
			e.log.Warn("the ClickHouse password is empty; the environment variable "+v+" is not set or is empty", zap.String("variable", v))
		} else {
			e.log.Warn("the ClickHouse password is empty; check the environment variable that the password setting reads (set password_variable to name it here)")
		}
		e.tel.configWarning(ctx, warnEmptyPassword)
	}
	bg, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.wg.Add(1)
	go e.connectLoop(bg)
	return nil
}

func (e *logsExporter) shutdown(context.Context) error {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.c == nil {
		return nil
	}
	err := e.c.Close()
	e.c = nil
	return err
}

// connectLoop dials until it succeeds, backing off up to 30 seconds, and warns once.
func (e *logsExporter) connectLoop(ctx context.Context) {
	defer e.wg.Done()
	delay := 500 * time.Millisecond
	warned := false
	for {
		c, err := e.dial(ctx)
		if err == nil {
			e.mu.Lock()
			e.c = c
			e.mu.Unlock()
			return
		}
		if !warned {
			e.log.Warn("cannot reach ClickHouse; connecting in the background, rows wait in the queue",
				zap.String("endpoint", e.cfg.Endpoint), zap.Error(err))
			warned = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func (e *logsExporter) conn() conn {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c
}

// pushLogs maps the records of one batch and inserts them in one native batch.
func (e *logsExporter) pushLogs(ctx context.Context, ld plog.Logs) error {
	rows := e.rows(ctx, ld)
	if len(rows) == 0 {
		return nil
	}
	return e.insertWithRetry(ctx, rows)
}

func (e *logsExporter) rows(ctx context.Context, ld plog.Logs) []row {
	var rows []row
	var invalid int64
	for i := range ld.ResourceLogs().Len() {
		scopes := ld.ResourceLogs().At(i).ScopeLogs()
		for j := range scopes.Len() {
			records := scopes.At(j).LogRecords()
			for k := range records.Len() {
				if r, ok := e.m.mapRecord(records.At(k)); ok {
					rows = append(rows, r)
				} else {
					invalid++
				}
			}
		}
	}
	if invalid > 0 {
		e.tel.drop(ctx, invalid, dropInvalid)
	}
	return rows
}

// insertWithRetry inserts the rows and, on a failure that can pass, retries with back-off until the
// configured time is spent. The batch is then dropped and counted. A schema error drops at once.
func (e *logsExporter) insertWithRetry(ctx context.Context, rows []row) error {
	bo := e.cfg.BackOffConfig
	start, interval := time.Now(), bo.InitialInterval
	for {
		err := e.insert(ctx, rows)
		if err == nil {
			e.tel.inserted.Add(ctx, int64(len(rows)))
			e.warnedSchema.Store(false)
			return nil
		}
		if isSchemaError(err) {
			e.tel.drop(ctx, int64(len(rows)), dropSchema)
			if e.warnedSchema.CompareAndSwap(false, true) {
				e.log.Warn("the flow table does not take the rows; dropping them", zap.Int("rows", len(rows)), zap.Error(err))
			}
			return consumererror.NewPermanent(err)
		}
		if !bo.Enabled || ctx.Err() != nil || time.Since(start)+interval > bo.MaxElapsedTime {
			e.tel.drop(ctx, int64(len(rows)), dropInsertFailed)
			e.log.Warn("insert failed; dropping the rows", zap.Int("rows", len(rows)), zap.Error(err))
			return consumererror.NewPermanent(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
		interval = min(time.Duration(float64(interval)*bo.Multiplier), bo.MaxInterval)
	}
}

// insert sends the rows in one prepared batch that names only the columns some row fills.
func (e *logsExporter) insert(ctx context.Context, rows []row) error {
	c := e.conn()
	if c == nil {
		return errNotConnected
	}
	cols := usedColumns(rows)
	b, err := c.PrepareBatch(ctx, e.statement(cols))
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = b.Close() }()
	for _, r := range rows {
		values := make([]any, len(cols))
		for i, col := range cols {
			if v, ok := r[col.name]; ok {
				values[i] = v
			} else {
				values[i] = col.kind.zero()
			}
		}
		if err := b.Append(values...); err != nil {
			return errAppend{err}
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// usedColumns are the table columns, in table order, that at least one row fills.
func usedColumns(rows []row) []column {
	var cols []column
	for _, col := range tableColumns {
		for _, r := range rows {
			if _, ok := r[col.name]; ok {
				cols = append(cols, col)
				break
			}
		}
	}
	return cols
}

func (e *logsExporter) statement(cols []column) string {
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = "`" + col.name + "`"
	}
	return fmt.Sprintf("INSERT INTO `%s`.`%s` (%s)", e.cfg.Database, e.cfg.Table, strings.Join(names, ", "))
}

// queueGuard turns a full sending queue into a counted drop, so the receiver is never refused.
type queueGuard struct {
	exporter.Logs
	tel *telemetry
}

func (q *queueGuard) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	err := q.Logs.ConsumeLogs(ctx, ld)
	if err != nil && strings.Contains(err.Error(), "sending queue is full") {
		q.tel.drop(ctx, int64(ld.LogRecordCount()), dropQueueFull)
		return nil
	}
	return err
}
