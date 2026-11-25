// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// wireAttr is one attribute of a fixture response; the encoder here is independent of the
// receiver's code.
type wireAttr struct {
	tag    byte
	name   string
	values [][]byte
}

func be32(v int32) []byte {
	u := uint32(v) //nolint:gosec // a fixture value
	return []byte{byte(u >> 24), byte(u >> 16), byte(u >> 8), byte(u)}
}

func enumAttr(name string, v int32) wireAttr {
	return wireAttr{0x23, name, [][]byte{be32(v)}}
}

func intAttr(name string, v int32) wireAttr {
	return wireAttr{0x21, name, [][]byte{be32(v)}}
}

func intsAttr(name string, vs ...int32) wireAttr {
	a := wireAttr{tag: 0x21, name: name}
	for _, v := range vs {
		a.values = append(a.values, be32(v))
	}
	return a
}

func boolAttr(name string, v bool) wireAttr {
	b := byte(0)
	if v {
		b = 1
	}
	return wireAttr{0x22, name, [][]byte{{b}}}
}

func stringsAttr(tag byte, name string, vs ...string) wireAttr {
	a := wireAttr{tag: tag, name: name}
	for _, v := range vs {
		a.values = append(a.values, []byte(v))
	}
	return a
}

func keywordAttr(name string, vs ...string) wireAttr { return stringsAttr(0x44, name, vs...) }

func namesAttr(name string, vs ...string) wireAttr { return stringsAttr(0x42, name, vs...) }

func u16(v int) []byte { return []byte{byte(v >> 8), byte(v)} }

func encodeResponse(status uint16, attrs ...wireAttr) []byte {
	b := []byte{1, 1, byte(status >> 8), byte(status), 0, 0, 0, 1, 0x01}
	for _, a := range []wireAttr{
		stringsAttr(0x47, "attributes-charset", "utf-8"),
		stringsAttr(0x48, "attributes-natural-language", "en"),
	} {
		b = appendWire(b, a)
	}
	b = append(b, 0x04)
	for _, a := range attrs {
		b = appendWire(b, a)
	}
	return append(b, 0x03)
}

func appendWire(b []byte, a wireAttr) []byte {
	for i, v := range a.values {
		name := a.name
		if i > 0 {
			name = ""
		}
		b = append(b, a.tag)
		b = append(b, u16(len(name))...)
		b = append(b, name...)
		b = append(b, u16(len(v))...)
		b = append(b, v...)
	}
	return b
}

// fakePrinter is an IPP server that records what it receives.
type fakePrinter struct {
	*httptest.Server

	mu        sync.Mutex
	ops       []uint16
	requested [][]string
	status    uint16
	attrs     []wireAttr
	delay     time.Duration
	block     chan struct{}
	once      sync.Once
}

func newFakePrinter(t *testing.T, attrs ...wireAttr) *fakePrinter {
	t.Helper()
	f := &fakePrinter{attrs: attrs}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	t.Cleanup(f.release)
	return f
}

// blocked makes every request wait until release.
func (f *fakePrinter) blocked() *fakePrinter {
	f.block = make(chan struct{})
	return f
}

func (f *fakePrinter) release() {
	f.once.Do(func() {
		if f.block != nil {
			close(f.block)
		}
	})
}

func (f *fakePrinter) set(status uint16, delay time.Duration, attrs ...wireAttr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.delay, f.attrs = status, delay, attrs
}

func (f *fakePrinter) endpoint() string {
	return "ipp://" + strings.TrimPrefix(f.URL, "http://") + "/ipp/print"
}

func (f *fakePrinter) recorded() (ops []uint16, requested [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.ops...), append([][]string(nil), f.requested...)
}

func (f *fakePrinter) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m, err := decodeMessage(body)
	f.mu.Lock()
	status, delay, attrs := f.status, f.delay, f.attrs
	if err == nil {
		f.ops = append(f.ops, m.code)
		f.requested = append(f.requested, requestedNames(m))
	}
	f.mu.Unlock()

	if f.block != nil {
		select {
		case <-f.block:
		case <-r.Context().Done():
			return
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/ipp")
	_, _ = w.Write(encodeResponse(status, attrs...))
}

func requestedNames(m message) []string {
	var names []string
	for _, g := range m.groups {
		for _, a := range g.attrs {
			if a.name != "requested-attributes" {
				continue
			}
			for _, v := range a.values {
				names = append(names, string(v.data))
			}
		}
	}
	return names
}

// healthyAttrs is a printer that is idle with full toner.
func healthyAttrs() []wireAttr {
	return []wireAttr{
		enumAttr("printer-state", 3),
		keywordAttr("printer-state-reasons", "none"),
		boolAttr("printer-is-accepting-jobs", true),
		intAttr("queued-job-count", 0),
		intsAttr("marker-levels", 90, 90, 90, 100),
		namesAttr("marker-names", "Black", "Cyan", "Magenta", "Yellow"),
		keywordAttr("marker-types", "toner", "toner", "toner", "toner"),
	}
}
