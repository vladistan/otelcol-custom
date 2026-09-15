// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filterlogprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor/processortest"
)

var testProcessorType = component.MustNewType("filterlog")

func TestProcessor_FilterlogEntry(t *testing.T) {
	// Create processor
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	// Set filterlog entry in original_message attribute
	filterlogEntry := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"
	lr.Attributes().PutStr("original_message", filterlogEntry)
	lr.Body().SetStr(filterlogEntry)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedLR := processedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	attrs := processedLR.Attributes()

	// Check OTEL semantic attributes
	val, ok := attrs.Get("network.interface.name")
	require.True(t, ok)
	assert.Equal(t, "em1", val.AsString())

	val, ok = attrs.Get("network.transport")
	require.True(t, ok)
	assert.Equal(t, "tcp", val.AsString())

	val, ok = attrs.Get("network.type")
	require.True(t, ok)
	assert.Equal(t, "ipv4", val.AsString())

	val, ok = attrs.Get("source.ip")
	require.True(t, ok)
	assert.Equal(t, "91.148.190.150", val.AsString())

	val, ok = attrs.Get("source.port")
	require.True(t, ok)
	assert.Equal(t, "50406", val.AsString())

	val, ok = attrs.Get("destination.ip")
	require.True(t, ok)
	assert.Equal(t, "24.2.177.2", val.AsString())

	val, ok = attrs.Get("destination.port")
	require.True(t, ok)
	assert.Equal(t, "47574", val.AsString())

	val, ok = attrs.Get("event.action")
	require.True(t, ok)
	assert.Equal(t, "block", val.AsString())

	// Check pf.* attributes
	val, ok = attrs.Get("pf.rule.id")
	require.True(t, ok)
	assert.Equal(t, "11", val.AsString())

	val, ok = attrs.Get("pf.rule.tracker")
	require.True(t, ok)
	assert.Equal(t, "02f4bab031b57d1e30553ce08e0ec131", val.AsString())

	val, ok = attrs.Get("pf.direction")
	require.True(t, ok)
	assert.Equal(t, "inbound", val.AsString())

	val, ok = attrs.Get("pf.reason")
	require.True(t, ok)
	assert.Equal(t, "match", val.AsString())

	val, ok = attrs.Get("pf.tcp.flags")
	require.True(t, ok)
	assert.Equal(t, "S", val.AsString())

	val, ok = attrs.Get("pf.ip.ttl")
	require.True(t, ok)
	assert.Equal(t, "244", val.AsString())

	// Check body was rewritten
	body := processedLR.Body().AsString()
	assert.Contains(t, body, "BLOCK")
	assert.Contains(t, body, "em1")
	assert.Contains(t, body, "inbound")
	assert.Contains(t, body, "tcp")
	assert.Contains(t, body, "91.148.190.150:50406")
	assert.Contains(t, body, "24.2.177.2:47574")
	assert.Contains(t, body, "SYN")
	assert.Contains(t, body, "rule 11")
}

func TestProcessor_NonFilterlog(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with non-filterlog message
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	originalBody := "This is a regular log message"
	lr.Body().SetStr(originalBody)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify - body should be unchanged
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedLR := processedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)

	assert.Equal(t, originalBody, processedLR.Body().AsString())

	// No filterlog attributes should be set
	_, ok := processedLR.Attributes().Get("pf.rule.id")
	assert.False(t, ok)
}

func TestProcessor_RewriteDisabled(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: false, // Disabled
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	filterlogEntry := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"
	lr.Body().SetStr(filterlogEntry)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify - body should NOT be rewritten
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedLR := processedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)

	// Body should still be the original
	assert.Equal(t, filterlogEntry, processedLR.Body().AsString())

	// But attributes should still be extracted
	val, ok := processedLR.Attributes().Get("pf.rule.id")
	require.True(t, ok)
	assert.Equal(t, "11", val.AsString())
}

func TestProcessor_UDP(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with UDP entry
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	filterlogEntry := "5,,,abc123def456,igb0,match,pass,out,4,0x0,,64,12345,0,DF,17,udp,60,192.168.1.100,8.8.8.8,54321,53,40"
	lr.Body().SetStr(filterlogEntry)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedLR := processedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	attrs := processedLR.Attributes()

	val, ok := attrs.Get("network.transport")
	require.True(t, ok)
	assert.Equal(t, "udp", val.AsString())

	val, ok = attrs.Get("event.action")
	require.True(t, ok)
	assert.Equal(t, "pass", val.AsString())

	val, ok = attrs.Get("pf.direction")
	require.True(t, ok)
	assert.Equal(t, "outbound", val.AsString())

	// No TCP flags for UDP
	_, ok = attrs.Get("pf.tcp.flags")
	assert.False(t, ok)
}

func TestProcessor_ICMP(t *testing.T) {
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with ICMP entry
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	filterlogEntry := "10,,,tracker123,em0,match,block,in,4,0x0,,128,54321,0,none,1,icmp,84,10.0.0.1,10.0.0.2,8,12345"
	lr.Body().SetStr(filterlogEntry)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedLR := processedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	attrs := processedLR.Attributes()

	val, ok := attrs.Get("network.transport")
	require.True(t, ok)
	assert.Equal(t, "icmp", val.AsString())

	val, ok = attrs.Get("pf.icmp.type")
	require.True(t, ok)
	assert.Equal(t, "8", val.AsString())

	val, ok = attrs.Get("pf.icmp.id")
	require.True(t, ok)
	assert.Equal(t, "12345", val.AsString())
}

func TestFactory(t *testing.T) {
	factory := NewFactory()
	assert.Equal(t, "filterlog", factory.Type().String())

	cfg := factory.CreateDefaultConfig()
	filterlogCfg := cfg.(*Config)
	assert.Equal(t, "original_message", filterlogCfg.SourceAttribute)
	assert.True(t, filterlogCfg.RewriteMessages)
}

func TestProcessor_ServiceNameSet(t *testing.T) {
	// Test that service.name is set to "filterlog" when processing filterlog entries
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with initial service.name set to something else (like syslog does)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "configd.py") // OPNsense configd
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()

	filterlogEntry := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"
	lr.Body().SetStr(filterlogEntry)

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify service.name was changed to "filterlog"
	require.Equal(t, 1, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	resourceAttrs := processedLogs.ResourceLogs().At(0).Resource().Attributes()

	val, ok := resourceAttrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "filterlog", val.AsString())
}

func TestProcessor_MixedBatch_SameResourceLog(t *testing.T) {
	// Test that when multiple log records are in the same ResourceLog batch,
	// filterlog entries get service.name="filterlog" while non-filterlog don't
	// NOTE: This tests the current behavior - when ANY log in a batch is filterlog,
	// the whole batch gets service.name="filterlog" because they share resource attrs.
	// This is acceptable for syslog where each message is typically its own ResourceLog.
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with multiple log records in the same ResourceLog
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "configd.py")
	sl := rl.ScopeLogs().AppendEmpty()

	// First log: regular message
	lr1 := sl.LogRecords().AppendEmpty()
	lr1.Body().SetStr("Regular syslog message")

	// Second log: filterlog entry
	lr2 := sl.LogRecords().AppendEmpty()
	filterlogEntry := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"
	lr2.Body().SetStr(filterlogEntry)

	// Third log: another regular message
	lr3 := sl.LogRecords().AppendEmpty()
	lr3.Body().SetStr("Another regular message")

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify
	require.Equal(t, 3, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]
	processedRL := processedLogs.ResourceLogs().At(0)

	// service.name should be "filterlog" because one of the logs was filterlog
	// This is expected behavior for shared resource attributes
	resourceAttrs := processedRL.Resource().Attributes()
	val, ok := resourceAttrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "filterlog", val.AsString())

	// Only the filterlog entry should have pf.* attributes
	processedSL := processedRL.ScopeLogs().At(0)

	// First log - no filterlog attributes
	lr1Attrs := processedSL.LogRecords().At(0).Attributes()
	_, ok = lr1Attrs.Get("pf.rule.id")
	assert.False(t, ok, "Regular log should not have pf.rule.id")

	// Second log - should have filterlog attributes
	lr2Attrs := processedSL.LogRecords().At(1).Attributes()
	val, ok = lr2Attrs.Get("pf.rule.id")
	assert.True(t, ok, "Filterlog entry should have pf.rule.id")
	assert.Equal(t, "11", val.AsString())

	// Third log - no filterlog attributes
	lr3Attrs := processedSL.LogRecords().At(2).Attributes()
	_, ok = lr3Attrs.Get("pf.rule.id")
	assert.False(t, ok, "Regular log should not have pf.rule.id")
}

func TestProcessor_SeparateResourceLogs(t *testing.T) {
	// Test that separate ResourceLogs maintain their own service.name
	// This is the typical case for syslog where each message is its own ResourceLog
	cfg := &Config{
		SourceAttribute: "original_message",
		RewriteMessages: true,
	}

	sink := new(consumertest.LogsSink)
	set := processortest.NewNopSettings(testProcessorType)
	proc, err := createLogsProcessor(context.Background(), set, cfg, sink)
	require.NoError(t, err)

	// Create test logs with separate ResourceLogs (typical syslog pattern)
	ld := plog.NewLogs()

	// First ResourceLog: regular syslog message
	rl1 := ld.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("service.name", "sshd")
	sl1 := rl1.ScopeLogs().AppendEmpty()
	lr1 := sl1.LogRecords().AppendEmpty()
	lr1.Body().SetStr("Accepted publickey for user")

	// Second ResourceLog: filterlog entry
	rl2 := ld.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("service.name", "configd.py")
	sl2 := rl2.ScopeLogs().AppendEmpty()
	lr2 := sl2.LogRecords().AppendEmpty()
	filterlogEntry := "11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss"
	lr2.Body().SetStr(filterlogEntry)

	// Third ResourceLog: another regular message
	rl3 := ld.ResourceLogs().AppendEmpty()
	rl3.Resource().Attributes().PutStr("service.name", "nginx")
	sl3 := rl3.ScopeLogs().AppendEmpty()
	lr3 := sl3.LogRecords().AppendEmpty()
	lr3.Body().SetStr("GET /index.html 200")

	// Process
	err = proc.ConsumeLogs(context.Background(), ld)
	require.NoError(t, err)

	// Verify
	require.Equal(t, 3, sink.LogRecordCount())
	processedLogs := sink.AllLogs()[0]

	// First ResourceLog: sshd - should remain unchanged
	rl1Attrs := processedLogs.ResourceLogs().At(0).Resource().Attributes()
	val, ok := rl1Attrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "sshd", val.AsString(), "sshd service.name should be unchanged")

	// Second ResourceLog: should be "filterlog" now
	rl2Attrs := processedLogs.ResourceLogs().At(1).Resource().Attributes()
	val, ok = rl2Attrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "filterlog", val.AsString(), "filterlog entry should have service.name=filterlog")

	// Third ResourceLog: nginx - should remain unchanged
	rl3Attrs := processedLogs.ResourceLogs().At(2).Resource().Attributes()
	val, ok = rl3Attrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "nginx", val.AsString(), "nginx service.name should be unchanged")
}
