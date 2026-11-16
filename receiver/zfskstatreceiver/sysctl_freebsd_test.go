// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build freebsd

package zfskstatreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests call the real kernel; they run on a FreeBSD host and are only
// compiled and vetted elsewhere. They use sysctls every FreeBSD kernel has.

func TestRealSysctlNameToOIDAndBack(t *testing.T) {
	oid, err := lookupOID("kern.ostype")
	require.NoError(t, err)
	require.NotEmpty(t, oid)

	name, err := oidName(oid)
	require.NoError(t, err)
	assert.Equal(t, "kern.ostype", name)
}

func TestRealSysctlUnknownNameIsNotFound(t *testing.T) {
	_, err := lookupOID("kern.no_such_sysctl_for_the_test")
	assert.Error(t, err)
}

func TestRealSysctlNextOIDWalksForward(t *testing.T) {
	oid, err := lookupOID("kern.ostype")
	require.NoError(t, err)

	next, err := nextOID(oid)
	require.NoError(t, err)
	require.NotEmpty(t, next)
	assert.NotEqual(t, oid, next)
	_, err = oidName(next)
	assert.NoError(t, err)
}

func TestRealSysctlStringAndBootTime(t *testing.T) {
	fns := realSysctl()

	s, err := fns.getString("kern.ostype")
	require.NoError(t, err)
	assert.Equal(t, "FreeBSD", s)

	boot, err := fns.bootTime()
	require.NoError(t, err)
	assert.False(t, boot.IsZero())
}

func TestRealSysctlUint64OfAnIntSysctl(t *testing.T) {
	v, err := realSysctl().getUint64("hw.ncpu")
	if err != nil {
		t.Skipf("hw.ncpu is not readable as uint64 here: %v", err)
	}
	assert.Positive(t, v)
}
