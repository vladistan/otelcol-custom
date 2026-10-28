// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"encoding/json"
	"os"
	"testing"
)

var defaultSuffixes = []string{"auth0.logs", "auth0.events"}

func TestDecodeEnvelopeAcceptsBothStreams(t *testing.T) {
	for _, stream := range defaultSuffixes {
		body := `{"source":"aws.partner/auth0.com/tenant/` + stream + `","account":"111122223333","region":"eu-central-1","detail":{"data":{"type":"s"}}}`
		env, err := decodeEnvelope(body, defaultSuffixes)
		if err != nil {
			t.Fatalf("%s rejected: %v", stream, err)
		}
		if env.Account != "111122223333" || env.Region != "eu-central-1" {
			t.Fatalf("%s: account/region = %q/%q", stream, env.Account, env.Region)
		}
	}
}

func TestDecodeEnvelopeRejects(t *testing.T) {
	tests := map[string]string{
		"not json":         `not json`,
		"other source":     `{"source":"aws.partner/other.com/t/other.logs","detail":{"data":{}}}`,
		"suffix mid-name":  `{"source":"aws.partner/auth0.com/t/auth0.logs.old","detail":{"data":{}}}`,
		"missing source":   `{"detail":{"data":{}}}`,
		"missing detail":   `{"source":"aws.partner/auth0.com/t/auth0.logs"}`,
		"detail not a map": `{"source":"aws.partner/auth0.com/t/auth0.logs","detail":"text"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEnvelope(body, defaultSuffixes); err == nil {
				t.Fatal("invalid message decoded")
			}
		})
	}
}

func TestDecodeEnvelopeLiveSample(t *testing.T) {
	env, err := decodeEnvelope(sampleBody(t, 0), defaultSuffixes)
	if err != nil {
		t.Fatalf("live sample rejected: %v", err)
	}
	if env.Region != "eu-central-1" {
		t.Fatalf("region = %q", env.Region)
	}
}

func sampleBody(t *testing.T, i int) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/auth0-event-samples.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []json.RawMessage
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	return string(samples[i])
}
