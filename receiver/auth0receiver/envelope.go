// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"encoding/json"
	"errors"
	"strings"
)

var errInvalidEnvelope = errors.New("not an Auth0 EventBridge envelope")

// envelope is the EventBridge wrapper around one Auth0 log event.
type envelope struct {
	Source  string          `json:"source"`
	Account string          `json:"account"`
	Time    string          `json:"time"`
	Region  string          `json:"region"`
	Detail  json.RawMessage `json:"detail"`
}

// decodeEnvelope accepts a message whose source ends in one of suffixes.
func decodeEnvelope(body string, suffixes []string) (envelope, error) {
	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return envelope{}, errors.Join(errInvalidEnvelope, err)
	}
	if !hasSuffix(env.Source, suffixes) {
		return envelope{}, errInvalidEnvelope
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(env.Detail, &detail); err != nil {
		return envelope{}, errors.Join(errInvalidEnvelope, err)
	}
	return env, nil
}

func hasSuffix(source string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(source, s) {
			return true
		}
	}
	return false
}
