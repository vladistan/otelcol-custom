package openvpnstatusreceiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

const (
	scopeName = "github.com/vladistan/otelcol-custom/receiver/openvpnstatusreceiver"

	// maxFileSize is 1 MiB; a status file is a few KiB per hundred clients.
	maxFileSize = 1 << 20
)

var errTooLarge = errors.New("status file is larger than 1 MiB")

type statusReceiver struct {
	logger *zap.Logger
	config *Config
	// Exactly one is set: the factory builds one receiver per signal.
	logs    consumer.Logs
	metrics consumer.Metrics

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Both fields belong to the poll goroutine.
	lastIdentity string
	lastProblem  string
}

func newStatusReceiver(settings receiver.Settings, cfg *Config, logs consumer.Logs, metrics consumer.Metrics) *statusReceiver {
	return &statusReceiver{logger: settings.Logger, config: cfg, logs: logs, metrics: metrics}
}

func (r *statusReceiver) Start(ctx context.Context, _ component.Host) error {
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.run(ctx)

	r.logger.Info("OpenVPN status receiver started",
		zap.String("path", r.config.Path),
		zap.Duration("poll_interval", r.config.PollInterval))
	return nil
}

func (r *statusReceiver) Shutdown(context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *statusReceiver) run(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()

	r.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

// poll reads the file once. Every failure waits for the next poll: the file
// is rewritten in place, so a read can land on a half-written snapshot.
func (r *statusReceiver) poll(ctx context.Context) {
	data, err := readCapped(r.config.Path)
	if err != nil {
		r.noteProblem(problemKind(err), err)
		return
	}
	snap, err := parseSnapshot(data)
	switch {
	case errors.Is(err, errEmpty):
		return
	case err != nil:
		r.noteProblem(problemKind(err), err)
		return
	}
	if len(snap.skipped) > 0 {
		r.noteProblem("skipped rows", fmt.Errorf("%d rows left out, first: %s", len(snap.skipped), snap.skipped[0]))
	} else {
		r.lastProblem = ""
	}

	if snap.identity == r.lastIdentity {
		return
	}
	if len(snap.clients) > 0 {
		if err := r.emit(ctx, snap); err != nil {
			r.logger.Error("Failed to send OpenVPN status data", zap.Error(err))
			return
		}
	}
	r.lastIdentity = snap.identity
}

func (r *statusReceiver) emit(ctx context.Context, snap snapshot) error {
	if r.logs != nil {
		return r.logs.ConsumeLogs(ctx, r.buildLogs(snap))
	}
	return r.metrics.ConsumeMetrics(ctx, r.buildMetrics(snap))
}

// readCapped reads at most maxFileSize bytes; a larger file is refused so a
// wrong path cannot load a big log into memory.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only; a close error changes nothing
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, errTooLarge
	}
	return data, nil
}

func problemKind(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	case errors.Is(err, errTooLarge):
		return "too large"
	case errors.Is(err, errIncomplete):
		return "partial"
	case errors.Is(err, errUnknownShape):
		return "unknown shape"
	default:
		return "unreadable"
	}
}

// noteProblem warns once per change of problem, not once per poll.
func (r *statusReceiver) noteProblem(kind string, err error) {
	if kind == r.lastProblem {
		return
	}
	r.lastProblem = kind
	r.logger.Warn("OpenVPN status input problem",
		zap.String("path", r.config.Path), zap.String("problem", kind), zap.Error(err))
}

func (r *statusReceiver) buildLogs(snap snapshot) plog.Logs {
	logs := plog.NewLogs()
	scope := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	scope.Scope().SetName(scopeName)

	var routes map[string][]route
	if r.config.JoinRoutingTable {
		routes = routesByName(snap.routes)
	}
	observed := pcommon.NewTimestampFromTime(time.Now())
	for _, c := range snap.clients {
		rec := scope.LogRecords().AppendEmpty()
		rec.SetTimestamp(pcommon.NewTimestampFromTime(snap.at))
		rec.SetObservedTimestamp(observed)
		attrs := rec.Attributes()
		attrs.PutStr("common_name", c.commonName)
		attrs.PutStr("real_address", c.ip)
		attrs.PutInt("bytes_received", c.bytesReceived)
		attrs.PutInt("bytes_sent", c.bytesSent)
		attrs.PutStr("connected_since", c.connectedSince)
		if match, ok := matchRoute(routes[c.commonName], c); ok {
			attrs.PutStr("virtual_address", match.virtualAddress)
			attrs.PutStr("last_ref", match.lastRef)
		}
	}
	return logs
}

func routesByName(routes []route) map[string][]route {
	byName := make(map[string][]route, len(routes))
	for _, rt := range routes {
		byName[rt.commonName] = append(byName[rt.commonName], rt)
	}
	return byName
}

// matchRoute joins on common name and real address, so a client never takes
// another session's row.
func matchRoute(candidates []route, c client) (route, bool) {
	for _, rt := range candidates {
		if rt.realAddress == c.realAddress {
			return rt, true
		}
	}
	return route{}, false
}
