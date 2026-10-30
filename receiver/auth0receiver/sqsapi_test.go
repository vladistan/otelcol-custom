// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

const (
	fakeAccessKey    = "env-chain-key-id-0001"
	fakeSigningValue = "env-chain-signing-value-7f3a9c"
	fakeToken        = "env-chain-session-b81d2e"
	assumedKeyID     = "assumed-key-id-0002"
	assumedSigning   = "assumed-signing-value-1c4e"
	assumedSession   = "assumed-session-9d2b"
	fakeExternal     = "fake-external-id-55aa"
)

const assumeRoleResponse = `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>assumed-key-id-0002</AccessKeyId><SecretAccessKey>assumed-signing-value-1c4e</SecretAccessKey><SessionToken>assumed-session-9d2b</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials><AssumedRoleUser><AssumedRoleId>role-id:otelcol-auth0</AssumedRoleId><Arn>arn:aws:sts::111122223333:assumed-role/auth0-reader/otelcol-auth0</Arn></AssumedRoleUser></AssumeRoleResult><ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata></AssumeRoleResponse>`

// fakeSTS records every request form it receives.
type fakeSTS struct {
	mu    sync.Mutex
	forms []url.Values
}

func (f *fakeSTS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	f.mu.Lock()
	f.forms = append(f.forms, form)
	f.mu.Unlock()
	if r.Header.Get("X-Amz-Target") != "" {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"__type":"InvalidAction","message":"fake queue refuses"}`)
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	_, _ = io.WriteString(w, assumeRoleResponse)
}

func (f *fakeSTS) requests() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.forms...)
}

func isolateAWSEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_ACCESS_KEY_ID", fakeAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", fakeSigningValue)
	t.Setenv("AWS_SESSION_TOKEN", fakeToken)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
}

func TestDefaultChainMakesNoSTSCall(t *testing.T) {
	isolateAWSEnv(t)
	sts := &fakeSTS{}
	srv := httptest.NewServer(sts)
	defer srv.Close()

	cfg := validConfig()
	cfg.Endpoint = srv.URL
	awsCfg, err := loadAWSConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := awsCfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != fakeAccessKey {
		t.Fatalf("access key = %q, want the env chain key", creds.AccessKeyID)
	}
	if n := len(sts.requests()); n != 0 {
		t.Fatalf("STS received %d requests, want 0", n)
	}
}

func TestRoleARNAssumesRoleWithExternalID(t *testing.T) {
	isolateAWSEnv(t)
	sts := &fakeSTS{}
	srv := httptest.NewServer(sts)
	defer srv.Close()

	cfg := validConfig()
	cfg.Endpoint = srv.URL
	cfg.RoleARN = "arn:aws:iam::111122223333:role/auth0-reader"
	cfg.ExternalID = fakeExternal
	awsCfg, err := loadAWSConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := awsCfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != assumedKeyID {
		t.Fatalf("access key = %q, want the assumed-role key", creds.AccessKeyID)
	}
	reqs := sts.requests()
	if len(reqs) != 1 {
		t.Fatalf("STS requests = %d, want 1", len(reqs))
	}
	form := reqs[0]
	if form.Get("Action") != "AssumeRole" || form.Get("RoleArn") != cfg.RoleARN || form.Get("ExternalId") != fakeExternal {
		t.Fatalf("AssumeRole form = %v", form)
	}
}

func TestRoleARNWithoutExternalIDOmitsIt(t *testing.T) {
	isolateAWSEnv(t)
	sts := &fakeSTS{}
	srv := httptest.NewServer(sts)
	defer srv.Close()

	cfg := validConfig()
	cfg.Endpoint = srv.URL
	cfg.RoleARN = "arn:aws:iam::111122223333:role/auth0-reader"
	awsCfg, err := loadAWSConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := awsCfg.Credentials.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := sts.requests()[0]["ExternalId"]; ok {
		t.Fatal("ExternalId sent although external_id is empty")
	}
}

func TestStartBuildsClientAndNoCredentialValueIsLogged(t *testing.T) {
	isolateAWSEnv(t)
	sts := &fakeSTS{}
	srv := httptest.NewServer(sts)
	defer srv.Close()

	h := newHarness(t)
	h.rcv.api = nil
	h.rcv.cfg.Endpoint = srv.URL
	h.rcv.cfg.RoleARN = "arn:aws:iam::111122223333:role/auth0-reader"
	h.rcv.cfg.ExternalID = fakeExternal
	h.rcv.cfg.QueueURL = srv.URL + "/111122223333/auth0-events"

	if err := h.rcv.Start(context.Background(), componenttest.NewNopHost()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The fake endpoint refuses SQS calls, so the loop logs errors.
	waitFor(t, func() bool { return len(sts.requests()) > 0 })
	waitFor(t, func() bool { return h.logs.Len() > 0 })
	if err := h.rcv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	h.rcv.logger.Info("config", zap.Any("note", "scan covers every captured entry"))

	secrets := []string{fakeAccessKey, fakeSigningValue, fakeToken, fakeExternal, assumedKeyID, assumedSigning, assumedSession}
	scanForSecrets(t, secrets, capturedText(h))
}

func TestNoCredentialValueInConsumedRecords(t *testing.T) {
	h := newHarness(t, message("m1", "h1", sampleBody(t, 0)), message("bad", "hb", "not json"))
	if err := h.rcv.pollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	scanForSecrets(t, []string{fakeAccessKey, fakeSigningValue, fakeToken, fakeExternal}, capturedText(h))
}

func capturedText(h *harness) string {
	var b strings.Builder
	for _, e := range h.logs.All() {
		fmt.Fprintf(&b, "%s %v\n", e.Message, e.ContextMap())
	}
	m := &plog.JSONMarshaler{}
	for _, l := range h.sink.AllLogs() {
		out, _ := m.MarshalLogs(l)
		b.Write(out)
	}
	return b.String()
}

func scanForSecrets(t *testing.T, secrets []string, text string) {
	t.Helper()
	if text == "" {
		t.Fatal("nothing captured to scan")
	}
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Fatalf("credential value %q found in captured output", s)
		}
	}
}
