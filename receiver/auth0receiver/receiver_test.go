// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// fakeSQS serves queued messages once and records deletes.
type fakeSQS struct {
	mu         sync.Mutex
	queue      []types.Message
	receiveErr []error
	deleted    []string
	failed     []types.BatchResultErrorEntry
	deleteErr  error
	onDelete   func()
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.receiveErr) > 0 {
		err := f.receiveErr[0]
		f.receiveErr = f.receiveErr[1:]
		return nil, err
	}
	out := &sqs.ReceiveMessageOutput{Messages: f.queue}
	f.queue = nil
	if len(out.Messages) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Millisecond):
		}
	}
	return out, nil
}

func (f *fakeSQS) DeleteMessageBatch(_ context.Context, in *sqs.DeleteMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onDelete != nil {
		f.onDelete()
	}
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	for _, e := range in.Entries {
		f.deleted = append(f.deleted, aws.ToString(e.ReceiptHandle))
	}
	return &sqs.DeleteMessageBatchOutput{Failed: f.failed}, nil
}

func (f *fakeSQS) deletedHandles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func message(id, handle, body string) types.Message {
	return types.Message{MessageId: aws.String(id), ReceiptHandle: aws.String(handle), Body: aws.String(body)}
}

type harness struct {
	rcv    *auth0Receiver
	api    *fakeSQS
	sink   *consumertest.LogsSink
	logs   *observer.ObservedLogs
	reader *sdkmetric.ManualReader
}

func newHarness(t *testing.T, msgs ...types.Message) *harness {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	reader := sdkmetric.NewManualReader()
	set := receivertest.NewNopSettings(component.MustNewType(typeStr))
	set.Logger = zap.New(core)
	set.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	cfg := validConfig()
	cfg.ErrorBackoff = time.Millisecond
	api := &fakeSQS{queue: msgs}
	sink := new(consumertest.LogsSink)
	rcv, err := newReceiver(cfg, set, sink, api)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{rcv: rcv, api: api, sink: sink, logs: logs, reader: reader}
}

func (h *harness) invalidCount(t *testing.T) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "auth0.messages.invalid" {
				return m.Data.(metricdata.Sum[int64]).DataPoints[0].Value
			}
		}
	}
	return 0
}

func TestPollDeletesOnlyAfterConsumerAccepts(t *testing.T) {
	h := newHarness(t, message("m1", "h1", sampleBody(t, 0)), message("m2", "h2", sampleBody(t, 1)))
	var seenAtDelete int
	h.api.onDelete = func() { seenAtDelete = h.sink.LogRecordCount() }

	if err := h.rcv.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if got := h.sink.LogRecordCount(); got != 2 {
		t.Fatalf("records = %d, want 2", got)
	}
	if seenAtDelete != 2 {
		t.Fatalf("delete ran with %d records consumed, want 2", seenAtDelete)
	}
	if got := h.api.deletedHandles(); len(got) != 2 || got[0] != "h1" || got[1] != "h2" {
		t.Fatalf("deleted = %v", got)
	}
	if len(h.sink.AllLogs()) != 1 {
		t.Fatalf("consumer calls = %d, want one batch", len(h.sink.AllLogs()))
	}
}

func TestRefusedConsumeDeletesNothing(t *testing.T) {
	h := newHarness(t, message("m1", "h1", sampleBody(t, 0)))
	h.rcv.next = consumertest.NewErr(errors.New("refused"))

	if err := h.rcv.pollOnce(context.Background()); err == nil {
		t.Fatal("pollOnce hid a refused consume")
	}
	if got := h.api.deletedHandles(); len(got) != 0 {
		t.Fatalf("deleted = %v, want none", got)
	}
}

func TestInvalidMessageStaysOnQueue(t *testing.T) {
	h := newHarness(t, message("bad-1", "hb", "not json"), message("m2", "h2", sampleBody(t, 0)))

	if err := h.rcv.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if got := h.api.deletedHandles(); len(got) != 1 || got[0] != "h2" {
		t.Fatalf("deleted = %v, want only h2", got)
	}
	warns := h.logs.FilterLevelExact(zap.WarnLevel).All()
	if len(warns) != 1 || warns[0].ContextMap()["message_id"] != "bad-1" {
		t.Fatalf("warnings = %v, want one naming bad-1", warns)
	}
	if got := h.invalidCount(t); got != 1 {
		t.Fatalf("auth0.messages.invalid = %d, want 1", got)
	}
}

func TestOnlyInvalidMessagesConsumeAndDeleteNothing(t *testing.T) {
	h := newHarness(t, message("bad-1", "hb", `{"source":"other"}`))

	if err := h.rcv.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if h.sink.LogRecordCount() != 0 || len(h.api.deletedHandles()) != 0 {
		t.Fatal("an invalid-only batch was consumed or deleted")
	}
}

func TestDeleteFailuresAreLoggedAndErrorsReturned(t *testing.T) {
	h := newHarness(t, message("m1", "h1", sampleBody(t, 0)))
	h.api.failed = []types.BatchResultErrorEntry{{Id: aws.String("0")}}
	if err := h.rcv.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if h.logs.FilterLevelExact(zap.WarnLevel).Len() != 1 {
		t.Fatal("failed delete entry not logged")
	}

	h = newHarness(t, message("m1", "h1", sampleBody(t, 0)))
	h.api.deleteErr = errors.New("boom")
	if err := h.rcv.pollOnce(context.Background()); err == nil {
		t.Fatal("delete error hidden")
	}
}

func TestReceiveErrorBacksOffAndRetries(t *testing.T) {
	h := newHarness(t, message("m1", "h1", sampleBody(t, 0)))
	h.api.receiveErr = []error{errors.New("throttled")}

	if err := h.rcv.Start(context.Background(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, func() bool { return h.sink.LogRecordCount() == 1 })
	if err := h.rcv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if h.logs.FilterLevelExact(zap.ErrorLevel).Len() < 1 {
		t.Fatal("receive error not logged")
	}
}

func TestShutdownWithoutStartIsNoop(t *testing.T) {
	h := newHarness(t)
	if err := h.rcv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestShutdownHonoursContext(t *testing.T) {
	h := newHarness(t)
	h.rcv.cancel = func() {}
	h.rcv.done = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.rcv.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown = %v, want context.Canceled", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
