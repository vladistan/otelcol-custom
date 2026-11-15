// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logrecombineprocessor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

// outRecord pairs the resource container.name with the body MESSAGE of an output record.
type outRecord struct{ container, message string }

// sinkConsumer collects the output records; it stands in for the next processor.
type sinkConsumer struct {
	mu   sync.Mutex
	recs []outRecord
}

func (*sinkConsumer) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (s *sinkConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		name, _ := rl.Resource().Attributes().Get("container.name")
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			lrs := rl.ScopeLogs().At(j).LogRecords()
			for k := 0; k < lrs.Len(); k++ {
				msg, _ := lrs.At(k).Body().Map().Get("MESSAGE")
				s.recs = append(s.recs, outRecord{container: name.Str(), message: msg.Str()})
			}
		}
	}
	return nil
}

func (s *sinkConsumer) records() []outRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outRecord(nil), s.recs...)
}

// journalLog is one journald entry as the receiver emits it: the fields are in the body.
// A blank containerID makes it a non-container entry, such as a line of the Docker daemon.
func journalLog(containerID, container, pid, msg string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("container.name", container)
	body := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetEmptyMap()
	body.PutStr("MESSAGE", msg)
	body.PutStr("_PID", pid)
	body.PutStr("_SYSTEMD_UNIT", "docker.service")
	if containerID != "" {
		body.PutStr("CONTAINER_ID", containerID)
	}
	return ld
}

func startProcessor(t *testing.T, mutate func(*Config)) (*logsProcessor, *sinkConsumer) {
	t.Helper()
	cfg := createDefaultConfig().(*Config)
	if mutate != nil {
		mutate(cfg)
	}
	sink := &sinkConsumer{}
	p, err := newLogsProcessor(processortest.NewNopSettings(component.MustNewType(typeStr)), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.Start(context.Background(), nil))
	t.Cleanup(func() { assert.NoError(t, p.Shutdown(context.Background())) })
	return p, sink
}

func send(t *testing.T, p *logsProcessor, ld plog.Logs) {
	t.Helper()
	require.NoError(t, p.ConsumeLogs(context.Background(), ld))
	time.Sleep(time.Millisecond)
}

// settle waits for the gap timer to release every buffer.
func settle() { time.Sleep(150 * time.Millisecond) }

func TestTwoContainersWithOnePIDStaySeparate(t *testing.T) {
	p, sink := startProcessor(t, nil)
	send(t, p, journalLog("aaaa", "busy-app", "900", "request served"))
	send(t, p, journalLog("bbbb", "otel-ac-test", "900", "hello"))
	send(t, p, journalLog("bbbb", "otel-ac-test", "900", "second line"))
	settle()

	assert.ElementsMatch(t, []outRecord{
		{"busy-app", "request served"},
		{"otel-ac-test", "hello\nsecond line"},
	}, sink.records())
}

func TestDaemonLineDoesNotAbsorbContainerLines(t *testing.T) {
	p, sink := startProcessor(t, nil)
	send(t, p, journalLog("", "", "900", "time=\"2026-11-15\" level=info msg=\"shim started\""))
	for i := 1; i <= 7; i++ {
		send(t, p, journalLog("bbbb", "otel-ac-test", "900", fmt.Sprintf("line %d", i)))
	}
	settle()

	assert.ElementsMatch(t, []outRecord{
		{"", "time=\"2026-11-15\" level=info msg=\"shim started\""},
		{"otel-ac-test", "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7"},
	}, sink.records())
}

func TestContainerContinuationLinesStillCombine(t *testing.T) {
	p, sink := startProcessor(t, nil)
	send(t, p, journalLog("bbbb", "py", "900", "2026-11-15T14:00:01 ERROR failed"))
	send(t, p, journalLog("bbbb", "py", "900", "Traceback (most recent call last):"))
	send(t, p, journalLog("bbbb", "py", "900", "ValueError: boom"))
	settle()

	assert.Equal(t, []outRecord{
		{"py", "2026-11-15T14:00:01 ERROR failed\nTraceback (most recent call last):\nValueError: boom"},
	}, sink.records())
}

func TestNonContainerEntriesStillGroupByPID(t *testing.T) {
	p, sink := startProcessor(t, nil)
	send(t, p, journalLog("", "", "42", "2026-11-15T14:00:01 service failed"))
	send(t, p, journalLog("", "", "42", "  detail line"))
	send(t, p, journalLog("", "", "43", "other process"))
	settle()

	assert.ElementsMatch(t, []outRecord{
		{"", "2026-11-15T14:00:01 service failed\n  detail line"},
		{"", "other process"},
	}, sink.records())
}

func TestEntriesWithoutPIDPassThrough(t *testing.T) {
	p, sink := startProcessor(t, nil)
	ld := journalLog("bbbb", "otel-ac-test", "900", "no pid")
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Map().Remove("_PID")
	send(t, p, ld)

	assert.Equal(t, []outRecord{{"otel-ac-test", "no pid"}}, sink.records())
}

func TestEmptyContainerIdentifierKeepsPIDGrouping(t *testing.T) {
	p, sink := startProcessor(t, func(cfg *Config) { cfg.ContainerIdentifier = "" })
	send(t, p, journalLog("aaaa", "busy-app", "900", "request served"))
	send(t, p, journalLog("bbbb", "otel-ac-test", "900", "hello"))
	settle()

	assert.Equal(t, []outRecord{{"busy-app", "request served\nhello"}}, sink.records())
}
