// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore expvarz ztype zlatencybucket zspanname Fflowlog

package flowlogexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/exporter/debugexporter"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/extension/zpagesextension"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	var last error
	for range 50 {
		resp, err := http.Get(url) //nolint:gosec // a test URL on loopback
		if err != nil {
			last = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)
		return string(body)
	}
	require.NoError(t, last)
	return ""
}

// The password is held as a configopaque.String. This runs the exporter through a refused login and
// scans everything an operator can look at for the value: the log lines, the error, the config in
// every print form and as the collector's config dump (confmap), the output of the real debug
// exporter at the most detailed level, the pages and expvar output of the real zpages extension, and
// the metric attributes.
func TestSecrets_NoOutputHoldsThePassword(t *testing.T) {
	cfg := testConfig()
	secret := string(cfg.Password)
	ctx := context.Background()

	// one tracer provider for the exporter and the zpages extension, so the extension shows its spans
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })
	set, logs, reader := rigSettings()
	set.TracerProvider = tp

	zpagesLogCore, zpagesLogs := observer.New(zap.DebugLevel)
	zpagesFactory := zpagesextension.NewFactory()
	zpagesCfg := zpagesFactory.CreateDefaultConfig().(*zpagesextension.Config)
	zpagesAddr := freeAddr(t)
	zpagesCfg.ServerConfig.NetAddr.Endpoint = zpagesAddr
	zpagesCfg.Expvar.Enabled = true
	zpagesSet := extensiontest.NewNopSettings(zpagesFactory.Type())
	zpagesSet.Logger = zap.New(zpagesLogCore)
	zpagesSet.TracerProvider = tp
	zpagesExt, err := zpagesFactory.Create(ctx, zpagesSet, zpagesCfg)
	require.NoError(t, err)
	require.NoError(t, zpagesExt.Start(ctx, componenttest.NewNopHost()))
	t.Cleanup(func() { _ = zpagesExt.Shutdown(ctx) })

	debugLogCore, debugLogs := observer.New(zap.DebugLevel)
	debugFactory := debugexporter.NewFactory()
	debugCfg := debugFactory.CreateDefaultConfig().(*debugexporter.Config)
	debugCfg.Verbosity = 3 // detailed
	debugSet := exportertest.NewNopSettings(debugFactory.Type())
	debugSet.Logger = zap.New(debugLogCore)
	debug, err := debugFactory.CreateLogs(ctx, debugSet, debugCfg)
	require.NoError(t, err)
	require.NoError(t, debug.Start(ctx, componenttest.NewNopHost()))

	// a connection that is up but refuses the login, so the push error path runs too
	refused := &fakeConn{prepareErr: errors.New("login refused")}
	e, err := buildLogs(ctx, set, cfg, func(context.Context) (conn, error) { return refused, nil })
	require.NoError(t, err)
	require.NoError(t, e.Start(ctx, componenttest.NewNopHost()))
	t.Cleanup(func() { _ = e.Shutdown(ctx) })

	batch := logsOf(flowAttrs(nil))
	require.NoError(t, e.ConsumeLogs(ctx, batch))
	require.NoError(t, debug.ConsumeLogs(ctx, batch))
	// the queue worker has run the failing push, so the error path is in the logs
	require.Eventually(t, func() bool { return countContaining(warnings(logs), "insert failed") > 0 }, 5*time.Second, time.Millisecond)

	var out strings.Builder
	section := func(name, text string) { fmt.Fprintf(&out, "\n== %s\n%s\n", name, text) }

	var entries strings.Builder
	for _, l := range logs.All() {
		fmt.Fprintf(&entries, "%s %v\n", l.Message, l.ContextMap())
	}
	section("exporter log", entries.String())

	var zpagesText strings.Builder
	for _, l := range zpagesLogs.All() {
		fmt.Fprintf(&zpagesText, "%s %v\n", l.Message, l.ContextMap())
	}
	section("zpages extension log", zpagesText.String())

	var debugText strings.Builder
	for _, l := range debugLogs.All() {
		fmt.Fprintf(&debugText, "%s %v\n", l.Message, l.ContextMap())
	}
	section("debug exporter output", debugText.String())

	section("config print forms", fmt.Sprintf("%v\n%+v\n%#v\n%s\n%v", cfg, cfg, cfg, cfg.Password, cfg.Password))

	dump := confmap.New()
	require.NoError(t, dump.Marshal(cfg))
	asJSON, err := json.Marshal(dump.ToStringMap())
	require.NoError(t, err)
	section("config dump (confmap)", fmt.Sprintf("%v\n%s", dump.ToStringMap(), asJSON))

	for _, page := range []string{"tracez", "expvarz"} {
		section("zpages "+page, fetch(t, "http://"+zpagesAddr+"/debug/"+page))
	}
	section("zpages tracez, one span name", fetch(t, "http://"+zpagesAddr+"/debug/tracez?zspanname=exporter%2Fflowlog%2Flogs&ztype=0&zlatencybucket=0"))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	var attrs strings.Builder
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					fmt.Fprintf(&attrs, "%s %v\n", m.Name, dp.Attributes.ToSlice())
				}
			}
		}
	}
	section("metric attributes", attrs.String())

	// each output holds something, so an empty capture cannot pass the scan
	for _, p := range strings.Split(out.String(), "\n== ")[1:] {
		name, body, _ := strings.Cut(p, "\n")
		assert.Greater(t, len(strings.TrimSpace(body)), 40, "the capture of %s is empty", name)
	}
	assert.NotContains(t, out.String(), secret, "the password is in some output")
	assert.NotContains(t, out.String(), strings.ToUpper(secret))
	assert.Contains(t, dump.ToStringMap(), "password", "the dump does list the password key, redacted")
	assert.Contains(t, fmt.Sprint(dump.ToStringMap()), "[REDACTED]")
}
