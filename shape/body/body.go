// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package body builds the one-line record bodies: "[component] text key=value".
package body

import (
	"strconv"
	"strings"
)

// Pair is one key=value tail entry.
type Pair struct {
	Key   string
	Value string
}

// Compose joins an optional [component] tag, the text and the non-empty pairs.
// A value with a space or a quote is quoted.
func Compose(component, text string, pairs ...Pair) string {
	parts := make([]string, 0, len(pairs)+2)
	if component != "" && text != "" {
		parts = append(parts, "["+component+"]")
	}
	if text != "" {
		parts = append(parts, text)
	}
	for _, p := range pairs {
		if p.Value == "" {
			continue
		}
		v := p.Value
		if strings.ContainsAny(v, " \t\"") {
			v = strconv.Quote(v)
		}
		parts = append(parts, p.Key+"="+v)
	}
	return strings.Join(parts, " ")
}

// Verb returns an HTTP or Kubernetes verb in uppercase.
func Verb(v string) string { return strings.ToUpper(v) }

// HTTPLine holds the parts of an access line body.
type HTTPLine struct {
	Method         string
	Host           string
	Path           string
	Query          string
	Status         int
	SizeBytes      int64
	DurationMicros int64
	HasDuration    bool
}

// HTTP builds "METHOD host/path query status size duration"; empty parts are left out.
func HTTP(l HTTPLine) string {
	target := l.Host + l.Path
	parts := []string{Verb(l.Method), target}
	if l.Query != "" {
		parts = append(parts, l.Query)
	}
	if l.Status != 0 {
		parts = append(parts, strconv.Itoa(l.Status))
	}
	if l.SizeBytes > 0 {
		parts = append(parts, FormatBytes(l.SizeBytes))
	}
	if l.HasDuration || l.DurationMicros > 0 {
		parts = append(parts, FormatDuration(l.DurationMicros))
	}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func oneDecimal(v float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
}

// FormatBytes writes a size as B, KiB or MiB with at most one decimal.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return oneDecimal(float64(n)/(1<<20)) + "MiB"
	case n >= 1<<10:
		return oneDecimal(float64(n)/(1<<10)) + "KiB"
	}
	return strconv.FormatInt(n, 10) + "B"
}

// FormatDuration writes microseconds as us, ms, s or m with at most one decimal.
func FormatDuration(micros int64) string {
	switch {
	case micros >= 60_000_000:
		m, s := micros/60_000_000, (micros%60_000_000)/1_000_000
		if s == 0 {
			return strconv.FormatInt(m, 10) + "m"
		}
		return strconv.FormatInt(m, 10) + "m" + strconv.FormatInt(s, 10) + "s"
	case micros >= 1_000_000:
		return oneDecimal(float64(micros)/1e6) + "s"
	case micros >= 1000 || micros == 0:
		return oneDecimal(float64(micros)/1e3) + "ms"
	}
	return strconv.FormatInt(micros, 10) + "us"
}

// Audit builds "ACTION target status" for the cloud audit logs.
func Audit(action, target, status string) string {
	var parts []string
	for _, p := range []string{action, target, status} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// OrOriginal returns shaped, or the original line when shaping gave nothing.
func OrOriginal(shaped, original string) string {
	if shaped == "" {
		return original
	}
	return shaped
}
