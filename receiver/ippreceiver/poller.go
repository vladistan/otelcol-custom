// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponse bounds the bytes read from one answer.
const maxResponse = 1 << 20

// poller reads one printer. One goroutine uses it, so it keeps plain fields.
type poller struct {
	name       string
	printerURI string
	url        string
	client     *http.Client
	requestID  uint32
	failing    bool
}

func newPoller(p Printer, timeout time.Duration) (*poller, error) {
	target, err := httpURL(p.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("printer %q: %w", p.Name, err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true // a printer closes idle connections its own way
	return &poller{
		name:       p.Name,
		printerURI: p.Endpoint,
		url:        target,
		client:     &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// fetch treats an answer without a printer attribute group as a failed poll.
func (p *poller) fetch(ctx context.Context) (map[string][]value, error) {
	p.requestID++
	body := encodeGetPrinterAttributes(p.printerURI, p.requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/ipp")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	m, err := decodeMessage(data)
	if err != nil {
		return nil, err
	}
	if m.code > statusSuccessMax {
		return nil, fmt.Errorf("IPP status 0x%04x", m.code)
	}
	attrs, found := m.printerAttributes()
	if !found {
		return nil, errors.New("answer has no printer attributes")
	}
	return attrs, nil
}
