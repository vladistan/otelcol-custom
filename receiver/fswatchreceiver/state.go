// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	stateVersion = 1
	maxRecent    = 256
	// maxDone bounds the files remembered as fully reported.
	maxDone = 4096
	// recentWindow is how far before the sentinel a file still counts as recently emitted.
	recentWindow = 2 * time.Second
)

type recentEntry struct {
	Path    string `json:"p"`
	Created int64  `json:"t"`
}

type persisted struct {
	V        int           `json:"v"`
	Sentinel int64         `json:"sentinel"`
	Recent   []recentEntry `json:"recent"`
	// Done lists files whose last record went out, so a restart does not send a second one.
	Done []recentEntry `json:"done,omitempty"`
}

type seenKey struct {
	path    string
	created int64
}

func marshalState(sentinel time.Time, recent, done []recentEntry) ([]byte, error) {
	if recent == nil {
		recent = []recentEntry{}
	}
	return json.Marshal(persisted{V: stateVersion, Sentinel: sentinel.UnixNano(), Recent: recent, Done: done})
}

func parseState(raw []byte) (persisted, error) {
	var st persisted
	if err := json.Unmarshal(raw, &st); err != nil {
		return persisted{}, fmt.Errorf("state is not valid JSON: %w", err)
	}
	if st.V != stateVersion {
		return persisted{}, fmt.Errorf("state version %d is not supported", st.V)
	}
	if st.Sentinel < 0 {
		return persisted{}, errors.New("state sentinel is negative")
	}
	return st, nil
}

// recentFrom keeps the newest files created within recentWindow before the sentinel.
func recentFrom(seen map[seenKey]time.Time, sentinel time.Time) []recentEntry {
	floor := sentinel.Add(-recentWindow).UnixNano()
	var out []recentEntry
	for k := range seen {
		if k.created >= floor {
			out = append(out, recentEntry{Path: k.path, Created: k.created})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created > out[j].Created
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > maxRecent {
		out = out[:maxRecent]
	}
	return out
}

// pruneDone deletes the entries of done created before floor or beyond the newest maxDone, and lists the rest.
func pruneDone(done map[seenKey]struct{}, floor time.Time) []recentEntry {
	var out []recentEntry
	for k := range done {
		if k.created < floor.UnixNano() {
			delete(done, k)
			continue
		}
		out = append(out, recentEntry{Path: k.path, Created: k.created})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created > out[j].Created
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > maxDone {
		for _, e := range out[maxDone:] {
			delete(done, seenKey{path: e.Path, created: e.Created})
		}
		out = out[:maxDone]
	}
	return out
}
