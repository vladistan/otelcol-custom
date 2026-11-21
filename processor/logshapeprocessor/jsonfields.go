// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// jsonObject is one decoded JSON object. It keeps the key order, which the debug exporter
// line and the attribute order rely on.
type jsonObject struct {
	keys []string
	vals map[string]any
}

// parseJSONObject decodes s as exactly one JSON object. Numbers become int64 or float64.
func parseJSONObject(s string) (*jsonObject, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := &jsonObject{vals: map[string]any{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("JSON key is not a string")
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.vals[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.vals[key] = normalizeJSON(v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the JSON object")
	}
	return o, nil
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		// A fraction or exponent that fits a float64 stays a float. A whole number beyond int64 and a
		// float too large for float64 keep their digits as text.
		if f, err := x.Float64(); err == nil && strings.ContainsAny(x.String(), ".eE") {
			return f
		}
		return x.String()
	case map[string]any:
		for k, e := range x {
			x[k] = normalizeJSON(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normalizeJSON(e)
		}
	}
	return v
}

func (o *jsonObject) has(key string) bool { _, ok := o.vals[key]; return ok }

// str returns a string value, or "" when the key is missing or not a string.
func (o *jsonObject) str(key string) string {
	s, _ := o.vals[key].(string)
	return s
}

func (o *jsonObject) object(key string) (map[string]any, bool) {
	m, ok := o.vals[key].(map[string]any)
	return m, ok
}

// take removes a key and returns its value.
func (o *jsonObject) take(key string) (any, bool) {
	v, ok := o.vals[key]
	if !ok {
		return nil, false
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return v, true
}

// takeStr removes a key and returns its string value, or "" when it is not a string.
func (o *jsonObject) takeStr(key string) string {
	v, _ := o.take(key)
	s, _ := v.(string)
	return s
}

// rest returns the remaining keys in their original order.
func (o *jsonObject) rest() []string { return o.keys }

// putAttrs writes every remaining key as an attribute, in order, under prefix+key.
func (o *jsonObject) putAttrs(m pcommon.Map, prefix string) {
	for _, k := range o.keys {
		putValue(m, prefix+k, o.vals[k])
	}
}

func putValue(m pcommon.Map, key string, v any) {
	if mv, ok := v.(map[string]any); ok && len(mv) == 0 {
		return
	}
	_ = m.PutEmpty(key).FromRaw(v)
}

// setSeverity writes a level to the record. A level the source does not give, or one the table
// does not know, is unset on purpose: it clears a severity a receiver set earlier.
func setSeverity(lr plog.LogRecord, l severity.Level) {
	lr.SetSeverityNumber(plog.SeverityNumber(l.Number))
	lr.SetSeverityText(l.Text)
}

// scalarText renders a decoded scalar for a body pair.
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(bytes.TrimSpace(b))
}

// parseTime reads a timestamp in one of the layouts of the formats of this wave.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unknown time layout " + strconv.Quote(s))
}

var (
	epochMin = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	epochMax = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// epochTime reads an epoch number: seconds (int or fraction) or milliseconds, by size. A value
// that is not a number, or that lands outside the years 2000 to 2099, is refused.
func epochTime(v any) (time.Time, bool) {
	var f float64
	switch x := v.(type) {
	case int64:
		f = float64(x)
	case float64:
		f = x
	default:
		return time.Time{}, false
	}
	var t time.Time
	if f > 1e11 {
		t = time.UnixMilli(int64(f)).UTC()
	} else {
		sec := int64(f)
		micros := int64((f-float64(sec))*1e6 + 0.5) // a float second keeps about a microsecond
		t = time.Unix(sec, micros*1000).UTC()
	}
	if t.Before(epochMin) || t.After(epochMax) {
		return time.Time{}, false
	}
	return t, true
}

// isNumeric reports whether a decoded value is a number, including one kept as text because it
// does not fit int64 or float64.
func isNumeric(v any) bool {
	switch x := v.(type) {
	case int64, float64:
		return true
	case string:
		_, err := strconv.ParseFloat(x, 64)
		return err == nil || errors.Is(err, strconv.ErrRange)
	}
	return false
}

// putOriginal keeps the shaped line in log.record.original and drops the copy a receiver may
// have put in original_message.
func putOriginal(a pcommon.Map, line string) {
	a.PutStr("log.record.original", line)
	a.Remove("original_message")
}

// splitCaller splits "path/file.go:12" into the path and the line number.
func splitCaller(caller string) (string, int64, bool) {
	i := strings.LastIndexByte(caller, ':')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(caller[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return caller[:i], n, true
}
