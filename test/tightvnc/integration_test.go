// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package tightvnc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/storage/filestorage"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"golang.org/x/text/encoding/unicode"

	"github.com/vladistan/otelcol-custom/processor/tightvncprocessor"
)

const (
	pollInterval = 20 * time.Millisecond
	settleTime   = 12 * pollInterval
	waitLimit    = 10 * time.Second

	// backlogSize is the size of the server log found on the first lab host.
	backlogSize = 434_314_372

	lineBuild     = "[ 6020/ 6484] 2026-11-10 21:47:52:697 + TightVNC Server Build on Jun  3 2026 at 14:25:03"
	lineStarting  = "[ 6020/ 6484] 2026-11-10 21:47:52:741 + Starting main RFB server"
	lineIncoming  = "[ 6020/ 6908] 2026-11-13 20:43:06:720 + Incoming rfb connection from 192.0.2.10 to port 5900"
	lineConnected = "[ 6020/ 6908] 2026-11-13 20:43:06:721 ! Client #0 connected"
	lineStopping  = "[ 6020/ 6484] 2026-11-13 20:50:00:100 + Stopping control server"
)

// storageHost serves the file_storage extension the receiver asks for by ID.
type storageHost struct {
	component.Host
	extensions map[component.ID]component.Component
}

func (h storageHost) GetExtensions() map[component.ID]component.Component { return h.extensions }

// utf16le encodes lines the way the server writes them: UTF-16LE, CRLF, and a BOM only at the
// start of a file.
func utf16le(t *testing.T, withBOM bool, lines ...string) []byte {
	t.Helper()
	policy := unicode.IgnoreBOM
	if withBOM {
		policy = unicode.UseBOM
	}
	var text string
	for _, l := range lines {
		text += l + "\r\n"
	}
	out, err := unicode.UTF16(unicode.LittleEndian, policy).NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return out
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func appendFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}

// collector is filelog (the reference config's receiver block) feeding the tightvnc processor.
type collector struct {
	sink     *consumertest.LogsSink
	receiver interface{ Shutdown(context.Context) error }
	storage  interface{ Shutdown(context.Context) error }
	stopOnce sync.Once
}

func startCollector(t *testing.T, logPath, storageDir string) *collector {
	t.Helper()
	ctx := context.Background()

	storageFactory := filestorage.NewFactory()
	storageCfg := storageFactory.CreateDefaultConfig().(*filestorage.Config)
	storageCfg.Directory = storageDir
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		t.Fatalf("create %s: %v", storageDir, err)
	}
	storage, err := storageFactory.Create(ctx, extensiontest.NewNopSettings(storageFactory.Type()), storageCfg)
	if err != nil {
		t.Fatalf("create file_storage: %v", err)
	}
	storageID := component.NewID(storageFactory.Type())
	host := storageHost{Host: componenttest.NewNopHost(), extensions: map[component.ID]component.Component{storageID: storage}}
	if err := storage.Start(ctx, host); err != nil {
		t.Fatalf("start file_storage: %v", err)
	}

	sink := new(consumertest.LogsSink)
	procFactory := tightvncprocessor.NewFactory()
	procCfg := procFactory.CreateDefaultConfig().(*tightvncprocessor.Config)
	procCfg.TimeZone = "UTC"
	proc, err := procFactory.CreateLogs(ctx, processortest.NewNopSettings(procFactory.Type()), procCfg, sink)
	if err != nil {
		t.Fatalf("create tightvnc: %v", err)
	}

	recvFactory := filelogreceiver.NewFactory()
	recvCfg := recvFactory.CreateDefaultConfig()
	block := map[string]any{
		"include":       []any{logPath},
		"encoding":      "utf-16le",
		"start_at":      "end",
		"storage":       storageID.String(),
		"poll_interval": pollInterval.String(),
	}
	if err := confmap.NewFromStringMap(block).Unmarshal(recvCfg); err != nil {
		t.Fatalf("unmarshal filelog block: %v", err)
	}
	// The storage key comes from the receiver ID, and the test settings give a new random one each
	// time; a restart finds its offsets only under a fixed ID.
	set := receivertest.NewNopSettings(recvFactory.Type())
	set.ID = component.NewIDWithName(recvFactory.Type(), "tightvnc")
	recv, err := recvFactory.CreateLogs(ctx, set, recvCfg, proc)
	if err != nil {
		t.Fatalf("create filelog: %v", err)
	}
	if err := recv.Start(ctx, host); err != nil {
		t.Fatalf("start filelog: %v", err)
	}
	c := &collector{sink: sink, receiver: recv, storage: storage}
	// A failed wait ends the test early; the cleanup still stops the collector.
	t.Cleanup(func() { c.stop(t) })
	return c
}

// stop shuts the receiver down first so its offsets are committed before storage closes.
func (c *collector) stop(t *testing.T) {
	t.Helper()
	c.stopOnce.Do(func() {
		ctx := context.Background()
		if err := c.receiver.Shutdown(ctx); err != nil {
			t.Errorf("shutdown filelog: %v", err)
		}
		if err := c.storage.Shutdown(ctx); err != nil {
			t.Errorf("shutdown file_storage: %v", err)
		}
	})
}

// bodies lists the body of every record that reached the sink, in arrival order.
func (c *collector) bodies() []string {
	var out []string
	for _, batch := range c.sink.AllLogs() {
		for ri := 0; ri < batch.ResourceLogs().Len(); ri++ {
			scopes := batch.ResourceLogs().At(ri).ScopeLogs()
			for si := 0; si < scopes.Len(); si++ {
				records := scopes.At(si).LogRecords()
				for i := 0; i < records.Len(); i++ {
					out = append(out, records.At(i).Body().Str())
				}
			}
		}
	}
	return out
}

func (c *collector) waitFor(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for len(c.bodies()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("got %d records %q, want %d", len(c.bodies()), c.bodies(), want)
		}
		time.Sleep(pollInterval)
	}
	// A duplicate would arrive on a later poll; give it the time to show.
	time.Sleep(settleTime)
}

func wantBodies(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("bodies = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bodies = %q, want %q", got, want)
		}
	}
}

func TestLineSplitAcrossACodeUnit_ArrivesOnceAndWhole(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tvnserver.log")
	writeFile(t, logPath, utf16le(t, true, lineBuild))
	c := startCollector(t, logPath, filepath.Join(dir, "storage"))
	time.Sleep(settleTime)

	whole := utf16le(t, false, lineIncoming, lineConnected)
	cut := len(whole)/2 | 1 // an odd offset lands inside a two-byte code unit
	appendFile(t, logPath, whole[:cut])
	time.Sleep(settleTime)
	appendFile(t, logPath, whole[cut:])
	c.waitFor(t, 2)

	wantBodies(t, c.bodies(),
		"Incoming rfb connection from 192.0.2.10 to port 5900", "Client #0 connected")
}

func TestCollectorRestart_ResumesFromTheStoredOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tvnserver.log")
	storageDir := filepath.Join(dir, "storage")
	writeFile(t, logPath, utf16le(t, true, lineBuild))
	first := startCollector(t, logPath, storageDir)
	time.Sleep(settleTime)
	appendFile(t, logPath, utf16le(t, false, lineIncoming, lineConnected))
	first.waitFor(t, 2)
	first.stop(t)

	appendFile(t, logPath, utf16le(t, false, lineStopping))
	second := startCollector(t, logPath, storageDir)
	second.waitFor(t, 1)

	wantBodies(t, second.bodies(), "Stopping control server")
}

func TestFirstStartOnAnExistingBacklog_DeliversNoLineOfIt(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a 434 MB sparse file")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tvnserver.log")
	writeFile(t, logPath, utf16le(t, true, lineBuild, lineStarting))
	if err := os.Truncate(logPath, backlogSize); err != nil {
		t.Fatalf("grow log to %d bytes: %v", backlogSize, err)
	}
	c := startCollector(t, logPath, filepath.Join(dir, "storage"))

	time.Sleep(4 * settleTime)
	if got := c.bodies(); len(got) != 0 {
		t.Fatalf("delivered %d lines of the existing backlog, want none: %q", len(got), got)
	}

	appendFile(t, logPath, utf16le(t, false, lineIncoming))
	c.waitFor(t, 1)

	wantBodies(t, c.bodies(), "Incoming rfb connection from 192.0.2.10 to port 5900")
}

func TestServiceRestartRotation_DeliversTheNewFilesBuildLineOnce(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tvnserver.log")
	writeFile(t, logPath, utf16le(t, true, lineBuild))
	c := startCollector(t, logPath, filepath.Join(dir, "storage"))
	time.Sleep(settleTime)
	appendFile(t, logPath, utf16le(t, false, lineIncoming))
	c.waitFor(t, 1)

	// The service restart renames the live file and starts a new one, BOM first.
	if err := os.Rename(logPath, filepath.Join(dir, "tvnserver.1.log")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	writeFile(t, logPath, utf16le(t, true, lineBuild, lineStarting))
	c.waitFor(t, 3)

	wantBodies(t, c.bodies(),
		"Incoming rfb connection from 192.0.2.10 to port 5900",
		"TightVNC Server Build on Jun  3 2026 at 14:25:03",
		"Starting main RFB server")
}
