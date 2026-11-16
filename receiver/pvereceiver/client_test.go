// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTokenID = "otel@pve!resources"
	testSecret  = "s3cr3t-token-value" // pragma: allowlist secret
)

func clientFor(t *testing.T, urls []string, ca string, insecure bool) *apiClient {
	t.Helper()
	cfg := &Config{BaseURLs: urls, CAFile: ca, InsecureSkipVerify: insecure}
	c, err := newAPIClient(cfg, testTokenID, testSecret)
	require.NoError(t, err)
	return c
}

func TestEveryRequestCarriesTheTokenHeader(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/version", `{"data":{"version":"8.0.3"}}`)
	f.ok("/api2/json/cluster/status", `{"data":[]}`)
	c := clientFor(t, []string{f.url()}, "", false)

	var out map[string]any
	require.NoError(t, c.get(context.Background(), "/version", time.Second, true, &out))
	require.NoError(t, c.get(context.Background(), "/cluster/status", time.Second, true, &out))

	assert.Equal(t, []string{
		"PVEAPIToken=" + testTokenID + "=" + testSecret,
		"PVEAPIToken=" + testTokenID + "=" + testSecret,
	}, f.tokenList())
}

func TestNextURLIsTriedAfterAConnectErrorAndAfterA503(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // nothing listens there now
	flaky := newFakePVE(t, false)
	flaky.set("/api2/json/version", http.StatusServiceUnavailable, "down")
	good := newFakePVE(t, false)
	good.ok("/api2/json/version", `{"data":{"version":"8.0.3"}}`)
	c := clientFor(t, []string{deadURL, flaky.url(), good.url()}, "", false)

	var out struct{ Data struct{ Version string } }
	require.NoError(t, c.get(context.Background(), "/version", time.Second, true, &out))

	assert.Equal(t, "8.0.3", out.Data.Version)
	assert.Equal(t, 1, flaky.countOf("/api2/json/version"))
	assert.Equal(t, 1, good.countOf("/api2/json/version"))
}

func TestAnErrorStatusIsNotRetriedOnAnotherURLWhenFailoverIsOff(t *testing.T) {
	first := newFakePVE(t, false)
	first.set("/api2/json/x", http.StatusInternalServerError, "QEMU guest agent is not running")
	second := newFakePVE(t, false)
	second.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{first.url(), second.url()}, "", false)

	var out map[string]any
	err := c.get(context.Background(), "/x", time.Second, false, &out)

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Status)
	assert.Contains(t, apiErr.Message, "not running")
	assert.Zero(t, second.countOf("/api2/json/x"))
}

func Test401And403AreReturnedAtOnce(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		first := newFakePVE(t, false)
		first.set("/api2/json/x", status, "Permission check failed (/vms/100, VM.Monitor)")
		second := newFakePVE(t, false)
		second.ok("/api2/json/x", `{"data":1}`)
		c := clientFor(t, []string{first.url(), second.url()}, "", false)

		var out map[string]any
		err := c.get(context.Background(), "/x", time.Second, true, &out)

		var apiErr *apiError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, status, apiErr.Status)
		assert.Zero(t, second.countOf("/api2/json/x"), "no failover on an auth answer")
	}
}

func TestSlowAnswerIsCutAtTheTimeout(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/x", `{"data":1}`)
	f.delay = 300 * time.Millisecond
	c := clientFor(t, []string{f.url()}, "", false)

	start := time.Now()
	var out map[string]any
	err := c.get(context.Background(), "/x", 50*time.Millisecond, true, &out)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 250*time.Millisecond)
}

func TestUntrustedCertificateFailsAndIsNotRetriedInsecurely(t *testing.T) {
	f := newFakePVE(t, true)
	f.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{f.url()}, "", false)

	var out map[string]any
	err := c.get(context.Background(), "/x", time.Second, true, &out)

	require.Error(t, err)
	assert.Zero(t, f.countOf("/api2/json/x"), "no request reached the handler")
	assert.NotContains(t, err.Error(), testSecret)
}

func TestCAFileMakesTheCertificateTrusted(t *testing.T) {
	f := newFakePVE(t, true)
	f.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{f.url()}, writeCA(t, f), false)

	var out map[string]any
	require.NoError(t, c.get(context.Background(), "/x", time.Second, true, &out))
}

func TestInsecureSkipVerifyIsAnExplicitOptIn(t *testing.T) {
	f := newFakePVE(t, true)
	f.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{f.url()}, "", true)

	var out map[string]any
	require.NoError(t, c.get(context.Background(), "/x", time.Second, true, &out))
}

func TestBadCAFileFailsAtCreation(t *testing.T) {
	_, err := newAPIClient(&Config{BaseURLs: []string{"https://h"}, CAFile: "/nonexistent.pem"}, testTokenID, testSecret)
	assert.Error(t, err)

	notPEM := t.TempDir() + "/x.pem"
	require.NoError(t, writeFile(notPEM, "not a certificate"))
	_, err = newAPIClient(&Config{BaseURLs: []string{"https://h"}, CAFile: notPEM}, testTokenID, testSecret)
	assert.Error(t, err)
}

func TestErrorTextNeverHoldsTheSecret(t *testing.T) {
	f := newFakePVE(t, false)
	f.set("/api2/json/x", http.StatusForbidden, "token "+testSecret+" is not allowed")
	c := clientFor(t, []string{f.url()}, "", false)

	var out map[string]any
	err := c.get(context.Background(), "/x", time.Second, true, &out)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), testSecret)
}

func TestUndecodableAnswerIsAnError(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/x", `not json`)
	c := clientFor(t, []string{f.url()}, "", false)

	var out map[string]any
	assert.Error(t, c.get(context.Background(), "/x", time.Second, true, &out))
}

func TestCancelledContextStopsTheRequest(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{f.url()}, "", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out map[string]any
	err := c.get(ctx, "/x", time.Second, true, &out)

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestTimeoutDoesNotMoveOnForTheGuestsClientButDoesForResources(t *testing.T) {
	slow := newFakePVE(t, false)
	slow.ok("/api2/json/x", `{"data":1}`)
	slow.delay = 300 * time.Millisecond
	fast := newFakePVE(t, false)
	fast.ok("/api2/json/x", `{"data":1}`)

	guests := clientFor(t, []string{slow.url(), fast.url()}, "", false)
	var out map[string]any
	err := guests.get(context.Background(), "/x", 50*time.Millisecond, false, &out)
	require.Error(t, err, "a timeout is the answer for a guest call")
	assert.Zero(t, fast.countOf("/api2/json/x"), "no second wait for the same stuck guest")

	resources := clientFor(t, []string{slow.url(), fast.url()}, "", false)
	require.NoError(t, resources.get(context.Background(), "/x", 50*time.Millisecond, true, &out))
	assert.Equal(t, 1, fast.countOf("/api2/json/x"), "resources moves on to the next URL")
}

func TestDialErrorMovesOnEvenForTheGuestsClient(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	good := newFakePVE(t, false)
	good.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{deadURL, good.url()}, "", false)

	var out map[string]any
	require.NoError(t, c.get(context.Background(), "/x", time.Second, false, &out))
}

func TestAnAnswerOverTheCapIsAClearError(t *testing.T) {
	old := maxBody
	maxBody = 64
	t.Cleanup(func() { maxBody = old })
	f := newFakePVE(t, false)
	f.ok("/api2/json/x", `{"data":"`+strings.Repeat("a", 200)+`"}`)
	c := clientFor(t, []string{f.url()}, "", false)

	var out map[string]any
	err := c.get(context.Background(), "/x", time.Second, true, &out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")
}

func TestCloseDropsTheIdleConnections(t *testing.T) {
	f := newFakePVE(t, false)
	f.ok("/api2/json/x", `{"data":1}`)
	c := clientFor(t, []string{f.url()}, "", false)
	var out map[string]any
	require.NoError(t, c.get(context.Background(), "/x", time.Second, true, &out))
	require.Zero(t, f.closedConns.Load(), "the connection is kept for the next request")

	c.close()

	require.Eventually(t, func() bool { return f.closedConns.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
}
