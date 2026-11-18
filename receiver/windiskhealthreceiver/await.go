// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"fmt"
	"math"
	"time"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// pendingCall is one overlapped control call that has not ended.
type pendingCall interface {
	wait(d time.Duration) (done bool, err error)
	cancel()
}

// awaitCall waits for a pending call at most timeout. After a timeout or a failed wait it cancels the call
// and waits at most grace for the cancel to land. finished tells whether the kernel is done with the buffers
// of the call; when it is false the caller must keep them alive.
func awaitCall(c pendingCall, timeout, grace time.Duration) (finished bool, err error) {
	done, waitErr := c.wait(timeout)
	if done && waitErr == nil {
		return true, nil
	}
	c.cancel()
	landed, graceErr := c.wait(grace)
	if graceErr != nil {
		landed = false // a failed wait proves nothing: keep the buffers alive
	}
	if waitErr != nil {
		return landed, fmt.Errorf("wait for the control call: %w", waitErr)
	}
	return landed, fmt.Errorf("%w: no answer in %s", smartdecode.ErrTimeout, timeout)
}

// timeoutMillis converts a duration to the milliseconds of a wait: at least 1, since 0 does not wait, and
// below the value that means wait forever.
func timeoutMillis(d time.Duration) uint32 {
	ms := d.Milliseconds()
	switch {
	case ms < 1:
		return 1
	case ms > math.MaxUint32-1:
		return math.MaxUint32 - 1
	}
	return uint32(ms)
}
