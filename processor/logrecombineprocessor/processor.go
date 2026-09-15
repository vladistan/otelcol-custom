// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package logrecombineprocessor combines multiline log entries (like stack traces)
// using time-based heuristics instead of brittle regex matching.
//
// Logic:
// 1. Accumulate journal messages in buckets by PID
// 2. If message starts with timestamp -> new multiline message -> release previous bucket
// 3. Otherwise keep accumulating
// 4. If > GapTimeout (10ms) since previous message -> release bucket
// 5. If > MaxTimeout (80ms) since first message -> release bucket
package logrecombineprocessor

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
)

// bufferedEntry holds a log record pending recombination
type bufferedEntry struct {
	record     plog.LogRecord
	resource   pcommon.Resource
	scope      pcommon.InstrumentationScope
	combinedTo string    // accumulated combined field value
	firstSeen  time.Time // when the first message arrived
	lastSeen   time.Time // when the last message arrived
}

// logsProcessor combines multiline log entries
type logsProcessor struct {
	config        *Config
	nextConsumer  consumer.Logs
	logger        *zap.Logger
	firstEntryRe  *regexp.Regexp
	combineField  []string // parsed field path (e.g., ["body", "MESSAGE"])
	sourceIdField []string // parsed field path (e.g., ["body", "_PID"])

	mu      sync.Mutex
	buffers map[string]*bufferedEntry // keyed by source identifier value
	ticker  *time.Ticker
	done    chan struct{}
}

func newLogsProcessor(set processor.Settings, cfg *Config, next consumer.Logs) (*logsProcessor, error) {
	re, err := regexp.Compile(cfg.IsFirstEntryPattern)
	if err != nil {
		return nil, err
	}

	return &logsProcessor{
		config:        cfg,
		nextConsumer:  next,
		logger:        set.Logger,
		firstEntryRe:  re,
		combineField:  parseFieldPath(cfg.CombineField),
		sourceIdField: parseFieldPath(cfg.SourceIdentifier),
		buffers:       make(map[string]*bufferedEntry),
		done:          make(chan struct{}),
	}, nil
}

// parseFieldPath splits a dotted field path into components
// e.g., "body.MESSAGE" -> ["body", "MESSAGE"]
func parseFieldPath(path string) []string {
	return strings.Split(path, ".")
}

func (p *logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *logsProcessor) Start(ctx context.Context, host component.Host) error {
	// Check frequently - every 5ms to catch the 10ms gap timeout
	p.ticker = time.NewTicker(5 * time.Millisecond)
	go p.flushLoop()
	return nil
}

func (p *logsProcessor) Shutdown(ctx context.Context) error {
	close(p.done)
	if p.ticker != nil {
		p.ticker.Stop()
	}

	// Flush remaining buffers
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.buffers) > 0 {
		logs := plog.NewLogs()
		for _, entry := range p.buffers {
			p.addEntryToLogs(logs, entry)
		}
		p.buffers = make(map[string]*bufferedEntry)
		if logs.LogRecordCount() > 0 {
			// Best effort flush on shutdown
			_ = p.nextConsumer.ConsumeLogs(ctx, logs)
		}
	}
	return nil
}

// flushLoop periodically flushes stale buffers based on time gaps
func (p *logsProcessor) flushLoop() {
	for {
		select {
		case <-p.done:
			return
		case <-p.ticker.C:
			p.flushStaleBuffers()
		}
	}
}

// flushStaleBuffers flushes buffers based on time gaps:
// - GapTimeout: time since last message (default 10ms)
// - MaxTimeout: time since first message (default 80ms)
func (p *logsProcessor) flushStaleBuffers() {
	p.mu.Lock()

	now := time.Now()
	var toFlush []*bufferedEntry
	var keysToDelete []string

	for key, entry := range p.buffers {
		timeSinceLast := now.Sub(entry.lastSeen)
		timeSinceFirst := now.Sub(entry.firstSeen)

		// Flush if gap between messages is too long OR total time is too long
		if timeSinceLast > p.config.GapTimeout || timeSinceFirst > p.config.MaxTimeout {
			toFlush = append(toFlush, entry)
			keysToDelete = append(keysToDelete, key)
		}
	}

	if len(toFlush) == 0 {
		p.mu.Unlock()
		return
	}

	// Remove flushed entries
	for _, key := range keysToDelete {
		delete(p.buffers, key)
	}

	// Build output logs
	logs := plog.NewLogs()
	for _, entry := range toFlush {
		p.addEntryToLogs(logs, entry)
	}

	// Release lock before sending downstream (avoid deadlock)
	p.mu.Unlock()

	if logs.LogRecordCount() > 0 {
		if err := p.nextConsumer.ConsumeLogs(context.Background(), logs); err != nil {
			p.logger.Warn("Failed to flush stale log entries", zap.Error(err))
		}
	}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Process incoming logs and build output
	outputLogs := plog.NewLogs()

	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				record := sl.LogRecords().At(k)
				p.processRecord(ctx, outputLogs, record, rl.Resource(), sl.Scope())
			}
		}
	}

	// Send any immediate output (flushed entries or pass-through)
	if outputLogs.LogRecordCount() > 0 {
		p.mu.Unlock()
		err := p.nextConsumer.ConsumeLogs(ctx, outputLogs)
		p.mu.Lock()
		return err
	}

	return nil
}

// processRecord handles a single log record using time-based heuristics:
// - If message starts with timestamp pattern -> flush previous bucket, start new one
// - Otherwise -> accumulate into existing bucket
func (p *logsProcessor) processRecord(ctx context.Context, outputLogs plog.Logs, record plog.LogRecord, resource pcommon.Resource, scope pcommon.InstrumentationScope) {
	now := time.Now()

	// Check for exclude attribute - if set to "true", pass through immediately
	if p.config.ExcludeAttribute != "" {
		if val, ok := record.Attributes().Get(p.config.ExcludeAttribute); ok && val.Str() == "true" {
			p.addRecordToLogs(outputLogs, record, resource, scope)
			return
		}
	}

	// Get source identifier value (PID)
	sourceId := p.getFieldValue(record, p.sourceIdField)
	if sourceId == "" {
		// No source identifier - pass through immediately
		p.addRecordToLogs(outputLogs, record, resource, scope)
		return
	}

	// Get combine field value (MESSAGE)
	combineValue := p.getFieldValue(record, p.combineField)
	if combineValue == "" {
		// No combine field - pass through immediately
		p.addRecordToLogs(outputLogs, record, resource, scope)
		return
	}

	// Check if this message starts with a timestamp (new log entry)
	isNewEntry := p.firstEntryRe.MatchString(combineValue)

	existing, hasBuffer := p.buffers[sourceId]

	if isNewEntry {
		// This starts a new log entry - flush any previous buffer for this PID
		if hasBuffer {
			p.addEntryToLogs(outputLogs, existing)
		}

		// Start new buffer with this entry
		newEntry := &bufferedEntry{
			record:     plog.NewLogRecord(),
			resource:   pcommon.NewResource(),
			scope:      pcommon.NewInstrumentationScope(),
			combinedTo: combineValue,
			firstSeen:  now,
			lastSeen:   now,
		}
		record.CopyTo(newEntry.record)
		resource.CopyTo(newEntry.resource)
		scope.CopyTo(newEntry.scope)
		p.buffers[sourceId] = newEntry

		// Check max sources limit
		if len(p.buffers) > p.config.MaxSources {
			p.evictOldestBuffer(outputLogs)
		}
	} else {
		// This is a continuation line (no timestamp)
		if hasBuffer {
			// Check if too much time has passed since last message
			if now.Sub(existing.lastSeen) > p.config.GapTimeout {
				// Gap too long - flush existing and start new buffer with this entry
				p.addEntryToLogs(outputLogs, existing)
				delete(p.buffers, sourceId)
				// Fall through to create new buffer below
			} else {
				// Append to existing buffer
				existing.combinedTo += p.config.CombineWith + combineValue
				existing.lastSeen = now

				// Check max batch size
				lines := strings.Count(existing.combinedTo, p.config.CombineWith) + 1
				if lines >= p.config.MaxBatchSize {
					// Flush due to size limit
					p.addEntryToLogs(outputLogs, existing)
					delete(p.buffers, sourceId)
				}
				return
			}
		}

		// No buffer for this source (or gap was too long) - start a new buffer
		// This handles continuation lines that arrive after buffer was flushed,
		// or error messages that don't start with timestamps (like "Traceback...")
		newEntry := &bufferedEntry{
			record:     plog.NewLogRecord(),
			resource:   pcommon.NewResource(),
			scope:      pcommon.NewInstrumentationScope(),
			combinedTo: combineValue,
			firstSeen:  now,
			lastSeen:   now,
		}
		record.CopyTo(newEntry.record)
		resource.CopyTo(newEntry.resource)
		scope.CopyTo(newEntry.scope)
		p.buffers[sourceId] = newEntry

		// Check max sources limit
		if len(p.buffers) > p.config.MaxSources {
			p.evictOldestBuffer(outputLogs)
		}
	}
}

// evictOldestBuffer removes and outputs the oldest buffered entry
func (p *logsProcessor) evictOldestBuffer(outputLogs plog.Logs) {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range p.buffers {
		if oldestKey == "" || entry.firstSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.firstSeen
		}
	}

	if oldestKey != "" {
		entry := p.buffers[oldestKey]
		p.addEntryToLogs(outputLogs, entry)
		delete(p.buffers, oldestKey)
	}
}

// getFieldValue retrieves a string value from a log record using a field path
func (p *logsProcessor) getFieldValue(record plog.LogRecord, fieldPath []string) string {
	if len(fieldPath) == 0 {
		return ""
	}

	switch fieldPath[0] {
	case "body":
		return p.getValueFromMap(record.Body(), fieldPath[1:])
	case "attributes":
		if len(fieldPath) > 1 {
			val, ok := record.Attributes().Get(fieldPath[1])
			if ok {
				return val.AsString()
			}
		}
	}
	return ""
}

// getValueFromMap extracts a value from a pcommon.Value (map or direct value)
func (p *logsProcessor) getValueFromMap(val pcommon.Value, path []string) string {
	if len(path) == 0 {
		return val.AsString()
	}

	if val.Type() == pcommon.ValueTypeMap {
		m := val.Map()
		if nextVal, ok := m.Get(path[0]); ok {
			return p.getValueFromMap(nextVal, path[1:])
		}
	}
	return ""
}

// setFieldValue sets a string value in a log record using a field path
func (p *logsProcessor) setFieldValue(record plog.LogRecord, fieldPath []string, value string) {
	if len(fieldPath) == 0 {
		return
	}

	switch fieldPath[0] {
	case "body":
		p.setValueInMap(record.Body(), fieldPath[1:], value)
	case "attributes":
		if len(fieldPath) > 1 {
			record.Attributes().PutStr(fieldPath[1], value)
		}
	}
}

// setValueInMap sets a value in a pcommon.Value (map)
func (p *logsProcessor) setValueInMap(val pcommon.Value, path []string, value string) {
	if len(path) == 0 {
		val.SetStr(value)
		return
	}

	if val.Type() != pcommon.ValueTypeMap {
		val.SetEmptyMap()
	}

	m := val.Map()
	if len(path) == 1 {
		m.PutStr(path[0], value)
	} else {
		nextVal, ok := m.Get(path[0])
		if !ok {
			nextVal = m.PutEmpty(path[0])
		}
		p.setValueInMap(nextVal, path[1:], value)
	}
}

// addEntryToLogs adds a buffered entry to the output logs
func (p *logsProcessor) addEntryToLogs(logs plog.Logs, entry *bufferedEntry) {
	// Set the combined field value before adding
	p.setFieldValue(entry.record, p.combineField, entry.combinedTo)

	rl := logs.ResourceLogs().AppendEmpty()
	entry.resource.CopyTo(rl.Resource())

	sl := rl.ScopeLogs().AppendEmpty()
	entry.scope.CopyTo(sl.Scope())

	newRecord := sl.LogRecords().AppendEmpty()
	entry.record.CopyTo(newRecord)
}

// addRecordToLogs adds a log record directly to the output logs (pass-through)
func (p *logsProcessor) addRecordToLogs(logs plog.Logs, record plog.LogRecord, resource pcommon.Resource, scope pcommon.InstrumentationScope) {
	rl := logs.ResourceLogs().AppendEmpty()
	resource.CopyTo(rl.Resource())

	sl := rl.ScopeLogs().AppendEmpty()
	scope.CopyTo(sl.Scope())

	newRecord := sl.LogRecords().AppendEmpty()
	record.CopyTo(newRecord)
}
