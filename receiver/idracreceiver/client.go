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
)

// reprobeAfter is how long a path stays marked absent. A 404 during an iDRAC reset must not hide a
// resource until the next restart.
const reprobeAfter = 6 * time.Hour

// statusError is an HTTP status the client did not expect; it matches errStatus.
type statusError struct {
	code int
	path string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s: %s answered %d", errStatus, e.path, e.code)
}
func (e *statusError) Is(target error) bool { return target == errStatus }

// client speaks Redfish to one iDRAC with a session token. It sends one request
// at a time, logs in again once after a 401, and remembers the paths the BMC
// answered 404 for.
type client struct {
	http       *http.Client
	base       string
	user, pass string
	reqTimeout time.Duration
	logger     *zap.Logger
	now        func() time.Time

	mu          sync.Mutex
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
	if c.token != "" {
		s = strings.ReplaceAll(s, c.token, "[redacted]")
	}
	return s
}

// login opens a session. The caller holds c.mu.
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
	c.token = resp.Header.Get("X-Auth-Token")
	if c.token == "" {
		return fmt.Errorf("%w: login gave no token", errStatus)
	}
	c.sessionPath = locationPath(resp.Header.Get("Location"))
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
	return errors.New(c.redact(err.Error()))
}

// get reads path into out. A 401 gives one new login and one retry; a 404
// marks the path absent for six hours and returns errAbsent without a request meanwhile.
func (c *client) get(ctx context.Context, path string, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.SplitN(path, "?", 2)[0]
	if since, ok := c.absent[key]; ok {
		if c.now().Sub(since) < reprobeAfter {
			return errAbsent
		}
		delete(c.absent, key)
	}
	for attempt := 0; ; attempt++ {
		if c.token == "" {
			if err := c.login(ctx); err != nil {
				return err
			}
		}
		resp, err := c.do(ctx, http.MethodGet, path, nil, c.token)
		if err != nil {
			return err
		}
		status := resp.StatusCode
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
		switch {
		case status == http.StatusUnauthorized && attempt == 0:
			c.token = ""
			continue
		case status == http.StatusUnauthorized:
			c.token = ""
			return fmt.Errorf("%w: %s answered 401 after a new login", errAuth, key)
		case status == http.StatusNotFound:
			c.absent[key] = c.now()
			c.logger.Info("Redfish path is absent on this BMC; not asked again for six hours", zap.String("path", key))
			return errAbsent
		case status/100 != 2:
			return &statusError{code: status, path: key}
		case readErr != nil:
			return c.classify(readErr)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: %s is not JSON: %s", errStatus, key, c.redact(err.Error()))
		}
		return nil
	}
}

// logout deletes the session, if there is one.
func (c *client) logout(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.http.CloseIdleConnections()
	if c.token == "" || c.sessionPath == "" {
		return
	}
	resp, err := c.do(ctx, http.MethodDelete, c.sessionPath, nil, c.token)
	c.token = ""
	if err != nil {
		c.logger.Debug("Redfish session delete failed", zap.String("error", c.redact(err.Error())))
		return
	}
	_ = resp.Body.Close()
}
