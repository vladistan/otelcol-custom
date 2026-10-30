// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// auth0Receiver polls one SQS queue and deletes messages after the consumer accepts them.
// Delivery is at-least-once: a failed delete redelivers the batch.
type auth0Receiver struct {
	cfg     *Config
	logger  *zap.Logger
	next    consumer.Logs
	api     sqsAPI
	invalid metric.Int64Counter

	cancel context.CancelFunc
	done   chan struct{}
}

func newReceiver(cfg *Config, set receiver.Settings, next consumer.Logs, api sqsAPI) (*auth0Receiver, error) {
	invalid, err := set.MeterProvider.Meter(typeStr).Int64Counter(
		"auth0.messages.invalid",
		metric.WithDescription("Queue messages that are not Auth0 EventBridge envelopes."),
	)
	if err != nil {
		return nil, err
	}
	return &auth0Receiver{cfg: cfg, logger: set.Logger, next: next, api: api, invalid: invalid}, nil
}

func (r *auth0Receiver) Start(ctx context.Context, _ component.Host) error {
	if r.api == nil {
		api, err := newSQSClient(ctx, r.cfg)
		if err != nil {
			return err
		}
		r.api = api
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		r.run(runCtx)
	}()
	return nil
}

func (r *auth0Receiver) Shutdown(ctx context.Context) error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *auth0Receiver) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := r.pollOnce(ctx)
		if err == nil || ctx.Err() != nil {
			continue
		}
		r.logger.Error("auth0 poll failed", zap.Error(err))
		select {
		case <-ctx.Done():
		case <-time.After(r.cfg.ErrorBackoff):
		}
	}
}

// pollOnce receives one batch, passes the valid records on in one call, then deletes them.
// Invalid messages stay on the queue for the redrive policy.
func (r *auth0Receiver) pollOnce(ctx context.Context) error {
	out, err := r.api.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(r.cfg.QueueURL),
		WaitTimeSeconds:     int32(r.cfg.WaitTimeSeconds),
		MaxNumberOfMessages: int32(r.cfg.MaxNumberOfMessages),
	})
	if err != nil {
		return fmt.Errorf("receive: %w", err)
	}

	logs := plog.NewLogs()
	var entries []types.DeleteMessageBatchRequestEntry
	for _, msg := range out.Messages {
		one, err := r.decode(aws.ToString(msg.Body))
		if err != nil {
			r.logger.Warn("invalid auth0 message left on queue", zap.String("message_id", aws.ToString(msg.MessageId)))
			r.invalid.Add(ctx, 1)
			continue
		}
		one.ResourceLogs().MoveAndAppendTo(logs.ResourceLogs())
		entries = append(entries, types.DeleteMessageBatchRequestEntry{
			Id:            aws.String(strconv.Itoa(len(entries))),
			ReceiptHandle: msg.ReceiptHandle,
		})
	}
	if len(entries) == 0 {
		return nil
	}

	if err := r.next.ConsumeLogs(ctx, logs); err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	res, err := r.api.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(r.cfg.QueueURL),
		Entries:  entries,
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	for _, f := range res.Failed {
		r.logger.Warn("auth0 message delete failed; it will be redelivered", zap.String("entry_id", aws.ToString(f.Id)))
	}
	return nil
}

func (r *auth0Receiver) decode(body string) (plog.Logs, error) {
	env, err := decodeEnvelope(body, r.cfg.SourcesSuffix)
	if err != nil {
		return plog.Logs{}, err
	}
	return mapEnvelope(env)
}
