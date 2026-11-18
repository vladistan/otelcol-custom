// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/entity"
	"go.opentelemetry.io/collector/processor/processortest"
)

type ciscoLine struct{ text, peer string }

func newSplitProcessor(sink *consumertest.LogsSink) *logsProcessor {
	return &logsProcessor{
		config:       &Config{SourceAttribute: "original_message", SetHostname: true, CleanBody: true},
		logger:       processortest.NewNopSettings(testProcessorType).Logger,
		nextConsumer: sink,
	}
}

func ciscoBatch(resource map[string]any, lines ...ciscoLine) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	if resource != nil {
		_ = rl.Resource().Attributes().FromRaw(resource)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("test-scope")
	for _, l := range lines {
		lr := sl.LogRecords().AppendEmpty()
		lr.Attributes().PutStr("original_message", l.text)
		if l.peer != "" {
			lr.Attributes().PutStr("net.peer.name", l.peer)
		}
		lr.Body().SetStr("<189>" + l.text)
	}
	return ld
}

type ciscoRecord struct {
	host, service, text, peer string
	resource                  map[string]any
}

func ciscoFlatten(ld plog.Logs) []ciscoRecord {
	var out []ciscoRecord
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		res := rl.Resource().Attributes().AsRaw()
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			recs := rl.ScopeLogs().At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				r := ciscoRecord{resource: res}
				r.host, _ = res["host.name"].(string)
				r.service, _ = res["service.name"].(string)
				v, _ := recs.At(k).Attributes().Get("original_message")
				r.text = v.Str()
				if peer, ok := recs.At(k).Attributes().Get("net.peer.name"); ok {
					r.peer = peer.Str()
				}
				out = append(out, r)
			}
		}
	}
	return out
}

const (
	linkDown = "12: Dec 15 20:49:14: %LINK-3-UPDOWN: Interface Gi1/0/1, changed state to down"
	sysCfg   = "13: Dec 15 20:49:15: %SYS-5-CONFIG_I: Configured from console"
)

func TestSplit_RecordsFromTwoPeersGetTheirOwnHostName(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	require.NoError(t, p.ConsumeLogs(context.Background(), ciscoBatch(nil,
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{linkDown, "switch-b"},
		ciscoLine{sysCfg, "switch-a"},
	)))

	got := ciscoFlatten(sink.AllLogs()[0])
	require.Len(t, got, 3)
	hosts := map[string]int{}
	for _, r := range got {
		hosts[r.host]++
	}
	assert.Equal(t, map[string]int{"switch-a": 2, "switch-b": 1}, hosts, "the first record no longer decides the host of the batch")
	for _, r := range got {
		assert.Equal(t, r.peer, r.host, "each record carries the host of its own peer")
	}
}

func TestSplit_OneHostWithTwoFacilitiesGetsTwoServiceNames(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	require.NoError(t, p.ConsumeLogs(context.Background(), ciscoBatch(nil,
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{sysCfg, "switch-a"},
	)))

	got := ciscoFlatten(sink.AllLogs()[0])
	require.Len(t, got, 2)
	services := map[string]string{}
	for _, r := range got {
		services[r.text] = r.service
		assert.Equal(t, "switch-a", r.host)
	}
	assert.Equal(t, "LINK", services[linkDown])
	assert.Equal(t, "SYS", services[sysCfg])
}

func TestSplit_ABatchOfOneHostAndFacilityStaysOneResourceInOrder(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	require.NoError(t, p.ConsumeLogs(context.Background(), ciscoBatch(map[string]any{"cloud.region": "r1"},
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{"14: Dec 15 20:49:16: %LINK-3-UPDOWN: Interface Gi1/0/2, changed state to down", "switch-a"},
	)))

	out := sink.AllLogs()[0]
	assert.Equal(t, 1, out.ResourceLogs().Len())
	got := ciscoFlatten(out)
	require.Len(t, got, 2)
	assert.Contains(t, got[1].text, "Gi1/0/2")
	assert.Equal(t, "r1", got[0].resource["cloud.region"])
	assert.Equal(t, "test-scope", out.ResourceLogs().At(0).ScopeLogs().At(0).Scope().Name())
}

func TestSplit_AHostAlreadyOnTheResourceIsKept(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	require.NoError(t, p.ConsumeLogs(context.Background(), ciscoBatch(map[string]any{"host.name": "relay-set", "service.name": "svc-set"},
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{sysCfg, "switch-b"},
	)))
	for _, r := range ciscoFlatten(sink.AllLogs()[0]) {
		assert.Equal(t, "relay-set", r.host, "an existing host.name is not overwritten")
		assert.Equal(t, "svc-set", r.service)
	}
}

func TestSplit_EntityReferencesAndDroppedCountReachEveryGroup(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	ld := ciscoBatch(map[string]any{"cloud.region": "r1"},
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{sysCfg, "switch-b"},
	)
	ref := entity.ResourceEntityRefs(ld.ResourceLogs().At(0).Resource()).AppendEmpty()
	ref.SetType("cloud")
	ld.ResourceLogs().At(0).Resource().SetDroppedAttributesCount(2)
	require.NoError(t, p.ConsumeLogs(context.Background(), ld))

	out := sink.AllLogs()[0]
	require.Equal(t, 2, out.ResourceLogs().Len())
	for i := 0; i < out.ResourceLogs().Len(); i++ {
		res := out.ResourceLogs().At(i).Resource()
		assert.Equal(t, 1, entity.ResourceEntityRefs(res).Len(), "resource %d keeps its entity reference", i)
		assert.Equal(t, uint32(2), res.DroppedAttributesCount())
	}
}

func TestSplit_AWideResourceStaysWholeInEveryGroup(t *testing.T) {
	sink := &consumertest.LogsSink{}
	p := newSplitProcessor(sink)
	wide := map[string]any{}
	for i := 0; i < 60; i++ {
		wide[fmt.Sprintf("resource.attribute.%02d", i)] = "v"
	}
	require.NoError(t, p.ConsumeLogs(context.Background(), ciscoBatch(wide,
		ciscoLine{linkDown, "switch-a"},
		ciscoLine{linkDown, "switch-b"},
	)))
	out := sink.AllLogs()[0]
	require.Equal(t, 2, out.ResourceLogs().Len())
	for i := 0; i < 2; i++ {
		assert.Equal(t, 62, out.ResourceLogs().At(i).Resource().Attributes().Len(), "60 attributes plus host.name and service.name")
	}
}
