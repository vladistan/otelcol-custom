// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type route struct {
	status int
	body   string
}

// fakePVE is a PVE API stand-in that records every request.
type fakePVE struct {
	srv *httptest.Server

	closedConns atomic.Int32

	mu          sync.Mutex
	routes      map[string]route
	hits        []string
	escaped     []string
	tokens      []string
	inflight    int
	maxInflight int
	delay       time.Duration
	delayOnly   string // when set, only paths holding it are delayed
}

func newFakePVE(t *testing.T, useTLS bool) *fakePVE {
	t.Helper()
	f := &fakePVE{routes: map[string]route{}}
	handler := http.HandlerFunc(f.serve)
	f.srv = httptest.NewUnstartedServer(handler)
	f.srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateClosed {
			f.closedConns.Add(1)
		}
	}
	if useTLS {
		f.srv.StartTLS()
	} else {
		f.srv.Start()
	}
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	f.hits = append(f.hits, r.URL.Path)
	f.escaped = append(f.escaped, r.URL.EscapedPath())
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	rt, ok := f.routes[r.URL.Path]
	delay := f.delay
	if f.delayOnly != "" && !strings.Contains(r.URL.Path, f.delayOnly) {
		delay = 0
	}
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
	}
	f.mu.Lock()
	f.inflight--
	f.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(rt.status)
	_, _ = w.Write([]byte(rt.body))
}

func (f *fakePVE) set(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = route{status, body}
}

func (f *fakePVE) ok(path, body string) { f.set(path, http.StatusOK, body) }

func (f *fakePVE) url() string { return f.srv.URL }

func (f *fakePVE) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func (f *fakePVE) escapedList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.escaped...)
}

func (f *fakePVE) tokenList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}

func (f *fakePVE) countOf(path string) int {
	n := 0
	for _, h := range f.hitList() {
		if h == path {
			n++
		}
	}
	return n
}

func (f *fakePVE) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInflight
}

// writeCA writes the certificate of a TLS fake as a PEM file and returns its path.
func writeCA(t *testing.T, f *fakePVE) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	cert := f.srv.Certificate()
	pem := "-----BEGIN CERTIFICATE-----\n" + base64Lines(cert.Raw) + "-----END CERTIFICATE-----\n"
	require.NoError(t, os.WriteFile(path, []byte(pem), 0o600))
	return path
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
