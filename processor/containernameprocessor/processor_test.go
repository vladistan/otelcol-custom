// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package containernameprocessor

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
)

const (
	journaldUnit = "docker.service"
	webName      = "web-1"
)

// logsOf returns one ResourceLogs per attribute map, each holding one record.
func logsOf(t *testing.T, resources ...map[string]any) plog.Logs {
	t.Helper()
	ld := plog.NewLogs()
	for _, attrs := range resources {
		rl := ld.ResourceLogs().AppendEmpty()
		if err := rl.Resource().Attributes().FromRaw(attrs); err != nil {
			t.Fatalf("set resource attributes: %v", err)
		}
		rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("line")
	}
	return ld
}

func newProcessor(t *testing.T, next *consumertest.LogsSink) processor.Logs {
	t.Helper()
	f := NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), next)
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}
	return p
}

// run sends ld through a fresh processor and returns the one batch the sink got.
func run(t *testing.T, ld plog.Logs) plog.Logs {
	t.Helper()
	sink := new(consumertest.LogsSink)
	if err := newProcessor(t, sink).ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(sink.AllLogs()) != 1 {
		t.Fatalf("sink got %d batches, want 1", len(sink.AllLogs()))
	}
	return sink.AllLogs()[0]
}

func resourceAttrs(t *testing.T, ld plog.Logs, i int) map[string]any {
	t.Helper()
	if i >= ld.ResourceLogs().Len() {
		t.Fatalf("batch has %d resources, want index %d", ld.ResourceLogs().Len(), i)
	}
	return ld.ResourceLogs().At(i).Resource().Attributes().AsRaw()
}

func TestConsumeLogs_ServiceNameFollowsContainerName(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "journald unit is overwritten",
			in:   map[string]any{"container.name": webName, "service.name": journaldUnit},
			want: map[string]any{"container.name": webName, "service.name": webName},
		},
		{
			name: "a non-docker service name is overwritten too",
			in:   map[string]any{"container.name": webName, "service.name": "sshd.service"},
			want: map[string]any{"container.name": webName, "service.name": webName},
		},
		{
			name: "a missing service name is set",
			in:   map[string]any{"container.name": webName},
			want: map[string]any{"container.name": webName, "service.name": webName},
		},
		{
			name: "no container name leaves the resource alone",
			in:   map[string]any{"service.name": journaldUnit},
			want: map[string]any{"service.name": journaldUnit},
		},
		{
			name: "an empty container name leaves the resource alone",
			in:   map[string]any{"container.name": "", "service.name": journaldUnit},
			want: map[string]any{"container.name": "", "service.name": journaldUnit},
		},
		{
			name: "a container name that is not a string leaves the resource alone",
			in:   map[string]any{"container.name": int64(7), "service.name": journaldUnit},
			want: map[string]any{"container.name": int64(7), "service.name": journaldUnit},
		},
		{
			name: "other resource attributes are kept",
			in:   map[string]any{"container.name": webName, "service.name": journaldUnit, "host.name": "h1"},
			want: map[string]any{"container.name": webName, "service.name": webName, "host.name": "h1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resourceAttrs(t, run(t, logsOf(t, tc.in)), 0)

			if len(got) != len(tc.want) {
				t.Fatalf("attributes = %v, want %v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s = %v, want %v", key, got[key], want)
				}
			}
		})
	}
}

func TestConsumeLogs_ContainerNameOnTheRecordIsIgnored(t *testing.T) {
	ld := logsOf(t, map[string]any{"service.name": journaldUnit})
	ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes().PutStr("container.name", webName)

	got := resourceAttrs(t, run(t, ld), 0)

	if got["service.name"] != journaldUnit {
		t.Errorf("service.name = %v, want %s", got["service.name"], journaldUnit)
	}
}

func TestConsumeLogs_EachResourceUsesItsOwnContainerName(t *testing.T) {
	ld := logsOf(t,
		map[string]any{"container.name": "web-1", "service.name": journaldUnit},
		map[string]any{"service.name": "sshd.service"},
		map[string]any{"container.name": "db-1", "service.name": journaldUnit},
	)

	out := run(t, ld)

	for i, want := range []string{"web-1", "sshd.service", "db-1"} {
		if got := resourceAttrs(t, out, i)["service.name"]; got != want {
			t.Errorf("resource %d service.name = %v, want %s", i, got, want)
		}
	}
}

func TestConsumeLogs_SecondPassChangesNothing(t *testing.T) {
	once := run(t, logsOf(t, map[string]any{"container.name": webName, "service.name": journaldUnit}))
	want := resourceAttrs(t, once, 0)

	twice := run(t, once)

	got := resourceAttrs(t, twice, 0)
	if len(got) != len(want) || got["service.name"] != want["service.name"] || got["container.name"] != want["container.name"] {
		t.Errorf("second pass gave %v, first pass gave %v", got, want)
	}
}

func TestConsumeLogs_RecordsAndBodiesPassThrough(t *testing.T) {
	ld := logsOf(t, map[string]any{"container.name": webName, "service.name": journaldUnit})

	out := run(t, ld)

	if out.LogRecordCount() != 1 {
		t.Fatalf("record count = %d, want 1", out.LogRecordCount())
	}
	if body := out.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str(); body != "line" {
		t.Errorf("body = %q, want %q", body, "line")
	}
}

func TestConsumeLogs_EmptyBatchPassesThrough(t *testing.T) {
	out := run(t, plog.NewLogs())

	if out.ResourceLogs().Len() != 0 {
		t.Errorf("resources = %d, want 0", out.ResourceLogs().Len())
	}
}

type failingConsumer struct{ err error }

func (f failingConsumer) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func (f failingConsumer) ConsumeLogs(context.Context, plog.Logs) error { return f.err }

func TestConsumeLogs_ReturnsTheNextConsumersError(t *testing.T) {
	want := errors.New("downstream refused")
	f := NewFactory()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), failingConsumer{err: want})
	if err != nil {
		t.Fatalf("create processor: %v", err)
	}

	got := p.ConsumeLogs(context.Background(), logsOf(t, map[string]any{"container.name": webName}))

	if !errors.Is(got, want) {
		t.Errorf("error = %v, want %v", got, want)
	}
}

func TestLifecycle_StartAndShutdownSucceed(t *testing.T) {
	p := newProcessor(t, new(consumertest.LogsSink))

	if err := p.Start(context.Background(), nil); err != nil {
		t.Errorf("start: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if !p.Capabilities().MutatesData {
		t.Error("Capabilities().MutatesData = false, want true")
	}
}
