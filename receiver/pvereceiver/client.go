// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/config/configopaque"
)

const (
	maxErrorBody = 4 << 10 // longest error text kept
	redacted     = "[REDACTED]"
)

// maxBody is the largest accepted answer. A variable so a test can lower it.
var maxBody int64 = 16 << 20

// apiError is an answer with an HTTP status other than 2xx.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("PVE answered HTTP %d", e.Status)
	}
	return fmt.Sprintf("PVE answered HTTP %d: %s", e.Status, e.Message)
}

// transportError is a failure before an answer arrived, with the token secret removed from its text.
type transportError struct {
	msg string
	err error
}

func (e *transportError) Error() string { return e.msg }
func (e *transportError) Unwrap() error { return e.err }

// apiClient calls the PVE API with one token. It tries the next base URL after a transport error.
type apiClient struct {
	urls   []string
	http   *http.Client
	auth   string
	secret string

	mu        sync.Mutex
	preferred int
}

func newAPIClient(cfg *Config, tokenID string, secret configopaque.String) (*apiClient, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // an explicit opt-in key
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %s holds no PEM certificate", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	return &apiClient{
		urls: cfg.BaseURLs,
		http: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		auth:   "PVEAPIToken=" + tokenID + "=" + string(secret),
		secret: string(secret),
	}, nil
}

func (c *apiClient) redact(text string) string {
	if c.secret == "" {
		return text
	}
	return strings.ReplaceAll(text, c.secret, redacted)
}

// close drops the idle connections of the client.
func (c *apiClient) close() { c.http.CloseIdleConnections() }

// isTimeout reports whether err is the per-request timeout or any other deadline.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// get reads path (below /api2/json) into out. A dial or TLS failure always moves on to the next URL. With
// failover a 5xx answer and a request timeout move on too; without it they are the answer, so a stuck guest
// does not cost one timeout per base URL.
func (c *apiClient) get(ctx context.Context, path string, timeout time.Duration, failover bool, out any) error {
	c.mu.Lock()
	start := c.preferred
	c.mu.Unlock()

	var lastErr error
	for i := range c.urls {
		idx := (start + i) % len(c.urls)
		err := c.getOne(ctx, c.urls[idx], path, timeout, out)
		if err == nil {
			c.mu.Lock()
			c.preferred = idx
			c.mu.Unlock()
			return nil
		}
		lastErr = err
		var apiErr *apiError
		if errors.As(err, &apiErr) && (!failover || apiErr.Status < 500) {
			return err
		}
		if ctx.Err() != nil || (!failover && isTimeout(err)) {
			return err
		}
	}
	return lastErr
}

func (c *apiClient) getOne(ctx context.Context, base, path string, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api2/json"+path, http.NoBody)
	if err != nil {
		return &transportError{msg: c.redact(err.Error()), err: err}
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return &transportError{msg: c.redact(err.Error()), err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &apiError{Status: resp.StatusCode, Message: c.redact(strings.TrimSpace(string(body)))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return &transportError{msg: "read the answer: " + c.redact(err.Error()), err: err}
	}
	if int64(len(body)) > maxBody {
		return &transportError{msg: fmt.Sprintf("the answer is larger than %d bytes", maxBody), err: errors.New("answer too large")}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &transportError{msg: "decode the answer: " + c.redact(err.Error()), err: err}
	}
	return nil
}
