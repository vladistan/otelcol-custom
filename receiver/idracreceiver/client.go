// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	sessionsPath = "/redfish/v1/SessionService/Sessions"
	maxBody      = 8 << 20
	expandQuery  = "?$expand=*($levels=1)"
)

// What the client reports; the target maps them to a reason.
var (
	errAuth    = errors.New("authentication failed")
	errTLS     = errors.New("TLS verification failed")
	errTimeout = errors.New("timed out")
	errAbsent  = errors.New("path absent")
	errStatus  = errors.New("unexpected status")
	errPanic   = errors.New("decoder failed")

	// errUndecodable is a member the BMC sent that does not decode; the member is skipped.
	errUndecodable = errors.New("member does not decode")
)

// reprobeAfter is how long a path stays marked absent. A 404 during an iDRAC reset must not hide a
// resource until the next restart.
const reprobeAfter = 6 * time.Hour

// neverLatch asks for a path to be requested on every read, whatever an earlier read answered. A 404 is then
// counted once, when the path first answers it, and the mark is dropped when the path answers again.
const neverLatch time.Duration = -1

// statusError is an HTTP status the client did not expect; it matches errStatus.
type statusError struct {
	code int
	path string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s: %s answered %d", errStatus, e.path, e.code)
}
func (e *statusError) Is(target error) bool { return target == errStatus }

// absentError is a 404. It matches errAbsent; fresh is true for the 404 that marked the path and false when
// the path was already marked, so a caller counts a path once.
type absentError struct{ fresh bool }

func (e *absentError) Error() string        { return errAbsent.Error() }
func (e *absentError) Is(target error) bool { return target == errAbsent }

// client speaks Redfish to one iDRAC with a session token. A scraper sends one request at a time, the base
// scrape and the storage read may overlap, a 401 gives one new login and one retry, and a path the BMC
// answered 404 for is remembered.
type client struct {
	http       *http.Client
	base       string
	user, pass string
	reqTimeout time.Duration
	logger     *zap.Logger
	now        func() time.Time

	loginMu     sync.Mutex // one login or logout at a time
	mu          sync.Mutex // guards the fields below
	token       string
	sessionPath string
	absent      map[string]time.Time // path to the time of its 404
}

func newClient(cfg TargetConfig, logger *zap.Logger, now func() time.Time) (*client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // an explicit opt-in
	if cfg.CAFile != "" {
		pemBytes, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("ca_file %s holds no certificate", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	return &client{
		http: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second},
			// A redirect would carry the token to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		base:       strings.TrimRight(cfg.Address, "/"),
		user:       cfg.Username,
		pass:       string(cfg.Password),
		reqTimeout: cfg.RequestTimeout,
		logger:     logger,
		now:        now,
		absent:     map[string]time.Time{},
	}, nil
}

// redact removes the password and the token from a text.
func (c *client) redact(s string) string {
	if c.pass != "" {
		s = strings.ReplaceAll(s, c.pass, "[redacted]")
	}
	if token := c.currentToken(); token != "" {
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	return s
}

func (c *client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// session returns the token, logging in first when there is none.
func (c *client) session(ctx context.Context) (string, error) {
	if token := c.currentToken(); token != "" {
		return token, nil
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if token := c.currentToken(); token != "" {
		return token, nil
	}
	if err := c.login(ctx); err != nil {
		return "", err
	}
	return c.currentToken(), nil
}

// dropToken forgets a token the BMC refused, unless another caller has already logged in again.
func (c *client) dropToken(stale string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == stale {
		c.token = ""
	}
}

// markAbsent remembers a 404 and reports whether the path was not marked already.
func (c *client) markAbsent(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, known := c.absent[key]
	c.absent[key] = c.now()
	return !known
}

// isAbsent reports whether the path answered 404 less than latch ago.
func (c *client) isAbsent(key string, latch time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	since, ok := c.absent[key]
	if !ok {
		return false
	}
	if c.now().Sub(since) < latch {
		return true
	}
	delete(c.absent, key)
	return false
}

// clearAbsent forgets a 404 of a path that answers again.
func (c *client) clearAbsent(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.absent, key)
}

// login opens a session. The caller holds c.loginMu.
func (c *client) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"UserName": c.user, "Password": c.pass})
	resp, err := c.do(ctx, http.MethodPost, sessionsPath, bytes.NewReader(body), "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: login answered %d", errAuth, resp.StatusCode)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("%w: login answered %d", errStatus, resp.StatusCode)
	}
	token := resp.Header.Get("X-Auth-Token")
	if token == "" {
		return fmt.Errorf("%w: login gave no token", errStatus)
	}
	c.mu.Lock()
	c.token, c.sessionPath = token, locationPath(resp.Header.Get("Location"))
	c.mu.Unlock()
	return nil
}

func locationPath(location string) string {
	if u, err := url.Parse(location); err == nil && u.Path != "" {
		return u.Path
	}
	return ""
}

// do sends one request under the request timeout. A transport failure is
// classified as a TLS or timeout error.
func (c *client) do(ctx context.Context, method, path string, body io.Reader, token string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.reqTimeout)
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, c.classify(err)
	}
	// The body is read under the same timeout: cancel when it is closed.
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func (c *client) classify(err error) error {
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	switch {
	case errors.As(err, &verify), errors.As(err, &unknown), strings.Contains(err.Error(), "x509:"):
		return fmt.Errorf("%w: %s", errTLS, c.redact(err.Error()))
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %s", errTimeout, c.redact(err.Error()))
	}
	return &redactedError{msg: c.redact(err.Error()), err: err}
}

// redactedError shows the redacted message of err and still matches it with errors.Is and errors.As.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// get reads path into out. A 401 gives one new login and one retry; a 404 marks the path absent for six
// hours, so it is not requested again meanwhile.
func (c *client) get(ctx context.Context, path string, out any) error {
	return c.getFor(ctx, path, out, reprobeAfter)
}

// getFor is get with the time a 404 keeps the path marked absent; neverLatch marks it for counting only.
func (c *client) getFor(ctx context.Context, path string, out any, latch time.Duration) error {
	key := strings.SplitN(path, "?", 2)[0]
	if latch > 0 && c.isAbsent(key, latch) {
		return &absentError{}
	}
	for attempt := 0; ; attempt++ {
		token, err := c.session(ctx)
		if err != nil {
			return err
		}
		resp, err := c.do(ctx, http.MethodGet, path, nil, token)
		if err != nil {
			return err
		}
		status := resp.StatusCode
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
		switch {
		case status == http.StatusUnauthorized && attempt == 0:
			c.dropToken(token)
			continue
		case status == http.StatusUnauthorized:
			c.dropToken(token)
			return fmt.Errorf("%w: %s answered 401 after a new login", errAuth, key)
		case status == http.StatusNotFound:
			fresh := c.markAbsent(key)
			if fresh {
				c.logger.Info("Redfish path is absent on this BMC", zap.String("path", key), zap.Duration("not_asked_again_for", max(latch, 0)))
			}
			return &absentError{fresh: fresh}
		case status/100 != 2:
			return &statusError{code: status, path: key}
		case readErr != nil:
			return c.classify(readErr)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: %s is not JSON: %s", errStatus, key, c.redact(err.Error()))
		}
		c.clearAbsent(key)
		return nil
	}
}

func (c *client) logout(ctx context.Context) {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	defer c.http.CloseIdleConnections()
	c.mu.Lock()
	token, sessionPath := c.token, c.sessionPath
	c.token = ""
	c.mu.Unlock()
	if token == "" || sessionPath == "" {
		return
	}
	resp, err := c.do(ctx, http.MethodDelete, sessionPath, nil, token)
	if err != nil {
		c.logger.Debug("Redfish session delete failed", zap.String("error", c.redact(err.Error())))
		return
	}
	_ = resp.Body.Close()
}
