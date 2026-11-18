// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// checker is a few assertions in the shape of testify, so this module keeps no test dependency.
// assert reports and goes on; require stops the test.
type checker struct{ stop bool }

var (
	assert  = checker{}
	require = checker{stop: true}
)

func (c checker) fail(t *testing.T, got string, msg []any) {
	t.Helper()
	text := got
	if len(msg) > 0 {
		text = fmt.Sprintf("%v: %s", fmt.Sprintf(fmt.Sprint(msg[0]), msg[1:]...), got)
	}
	if c.stop {
		t.Fatal(text)
	}
	t.Error(text)
}

func (c checker) Equal(t *testing.T, want, got any, msg ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		c.fail(t, fmt.Sprintf("want %#v, got %#v", want, got), msg)
	}
}

func (c checker) NoError(t *testing.T, err error, msg ...any) {
	t.Helper()
	if err != nil {
		c.fail(t, fmt.Sprintf("unexpected error: %v", err), msg)
	}
}

func (c checker) Error(t *testing.T, err error, msg ...any) {
	t.Helper()
	if err == nil {
		c.fail(t, "want an error, got none", msg)
	}
}

func (c checker) ErrorIs(t *testing.T, err, target error, msg ...any) {
	t.Helper()
	if !errors.Is(err, target) {
		c.fail(t, fmt.Sprintf("error %v is not %v", err, target), msg)
	}
}

func (c checker) True(t *testing.T, ok bool, msg ...any) {
	t.Helper()
	if !ok {
		c.fail(t, "want true", msg)
	}
}

func (c checker) False(t *testing.T, ok bool, msg ...any) {
	t.Helper()
	if ok {
		c.fail(t, "want false", msg)
	}
}

func (c checker) Zero(t *testing.T, v any, msg ...any) {
	t.Helper()
	if !reflect.ValueOf(v).IsZero() {
		c.fail(t, fmt.Sprintf("want the zero value, got %#v", v), msg)
	}
}

func (c checker) NotPanics(t *testing.T, fn func(), msg ...any) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			c.fail(t, fmt.Sprintf("panic: %v", r), msg)
		}
	}()
	fn()
}
