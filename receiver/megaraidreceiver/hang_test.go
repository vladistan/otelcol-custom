// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestAHungControllerIsNotOpenedAgainUntilTheCallReturns(t *testing.T) {
	release := make(chan struct{})
	var opens, ioctls, closed atomic.Int32
	core, logs := observer.New(zapcore.DebugLevel)
	clock := &testClock{t: time.Unix(1_700_000_000, 0)}
	s := newScraper(zap.New(core), testConfig(),
		func() ([]controller, error) { return []controller{testCtrl}, nil },
		func(int) (device, func(), error) {
			opens.Add(1)
			d := newIoctlDevice(func([]byte, ...[]byte) error { ioctls.Add(1); <-release; return nil })
			d.timeout = 30 * time.Millisecond
			return d, func() { closed.Add(1) }, nil
		}, clock.now)

	for range 10 {
		s.scrape(context.Background())
		clock.t = clock.t.Add(time.Minute)
	}

	assert.Equal(t, int32(1), opens.Load(), "a stuck controller is opened once, not every scrape")
	assert.Equal(t, int32(1), ioctls.Load(), "one call is outstanding, never more")
	assert.Zero(t, closed.Load(), "the node stays open while the kernel holds the call")
	assert.Equal(t, 2, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len(), "one line for the timeout, one for the wait")

	close(release)
	require.Eventually(t, func() bool { return closed.Load() == 1 }, 2*time.Second, 5*time.Millisecond, "the waiter closes the node after the call returns")
	s.scrape(context.Background())
	assert.Equal(t, int32(2), opens.Load(), "the controller is read again once the call has returned")
}

func TestATimedOutDeviceReportsWhenTheKernelReturns(t *testing.T) {
	release := make(chan struct{})
	d := newIoctlDevice(func([]byte, ...[]byte) error { <-release; return nil })
	d.timeout = 20 * time.Millisecond
	assert.Nil(t, d.hung())

	_, err := d.exec(request{opcode: opCtrlGetInfo, dataLen: 8})

	require.ErrorIs(t, err, errTimeout)
	ch := d.hung()
	require.NotNil(t, ch)
	select {
	case <-ch:
		t.Fatal("the channel closed while the call was outstanding")
	default:
	}
	close(release)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("the channel did not close after the call returned")
	}
}

func TestOpenErrorsSeparateFileAccessFromTheCapability(t *testing.T) {
	access := mapOpenError(syscall.EACCES, "mode 0600 uid 0 gid 0")
	require.ErrorIs(t, access, errNodeAccess)
	assert.Contains(t, access.Error(), "mode 0600 uid 0 gid 0")
	assert.Contains(t, access.Error(), "file access")
	assert.Contains(t, access.Error(), "CAP_SYS_ADMIN")
	assert.True(t, skipsScrape(access))
	assert.Equal(t, "access", errClass(access))

	capability := mapOpenError(syscall.EPERM, "mode 0600 uid 0 gid 0")
	require.ErrorIs(t, capability, errNeedsCapability)
	assert.NotErrorIs(t, capability, errNodeAccess)
	assert.Equal(t, "capability", errClass(capability))

	other := mapOpenError(syscall.EMFILE, "")
	assert.False(t, skipsScrape(other))
	assert.Contains(t, errNodeMissing.Error(), "create_node")
}
