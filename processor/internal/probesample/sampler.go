// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package probesample

import (
	"math/rand/v2"
	"strings"
)

// Sampler classifies health probes and decides which to keep.
type Sampler struct {
	paths  map[string]struct{}
	agents []string
	ratio  float64
	draw   func() float64
}

// New builds a Sampler. draw returns values in [0, 1); nil uses math/rand/v2.
func New(cfg Config, draw func() float64) *Sampler {
	if draw == nil {
		draw = rand.Float64
	}
	paths := make(map[string]struct{}, len(cfg.Paths))
	for _, p := range cfg.Paths {
		paths[p] = struct{}{}
	}
	return &Sampler{paths: paths, agents: cfg.Agents, ratio: cfg.KeepRatio, draw: draw}
}

// Classify reports whether a record is a health probe: status 200 or 302, and
// either a listed path (query string and one trailing slash ignored) or a listed agent prefix.
func (s *Sampler) Classify(status int, path, userAgent string) bool {
	if status != 200 && status != 302 {
		return false
	}
	if path != "" {
		path, _, _ = strings.Cut(path, "?")
		if _, ok := s.paths[path]; ok {
			return true
		}
		if _, ok := s.paths[strings.TrimSuffix(path, "/")]; ok {
			return true
		}
	}
	for _, prefix := range s.agents {
		if strings.HasPrefix(userAgent, prefix) {
			return true
		}
	}
	return false
}

// Keep draws once and reports whether the probe stays.
func (s *Sampler) Keep() bool {
	switch {
	case s.ratio >= 1:
		return true
	case s.ratio <= 0:
		return false
	}
	return s.draw() < s.ratio
}
