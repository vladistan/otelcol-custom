// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "errors"

// Errors a transport or a decoder returns. Callers match them with errors.Is.
var (
	// ErrCommandNotAllowed means the command is not on the read-only allow list.
	ErrCommandNotAllowed = errors.New("command is not on the read-only allow list")
	// ErrShortBuffer means a buffer is shorter than its layout needs.
	ErrShortBuffer = errors.New("buffer is shorter than the layout needs")
	// ErrUnsupported means the device or the bridge in front of it refuses the command path.
	ErrUnsupported = errors.New("device does not support the command path")
	// ErrTimeout means a command did not finish in time.
	ErrTimeout = errors.New("command timed out")
	// ErrPermission means the process may not send the command: the kernel refused the ioctl for want
	// of a capability, or the device file could not be opened.
	ErrPermission = errors.New("permission denied")
	// ErrFileAccess means the device file could not be opened for reading. It comes together with
	// ErrPermission: the process needs read access to the file before any capability matters.
	ErrFileAccess = errors.New("no read access to the device file")
	// ErrSMARTUnsupported means the device has no SMART feature set.
	ErrSMARTUnsupported = errors.New("SMART is not supported by the device")
	// ErrSMARTDisabled means SMART is switched off on the device; the decoders never switch it on.
	ErrSMARTDisabled = errors.New("SMART is disabled on the device")
	// ErrStandby means a rotating device is in standby and was left alone.
	ErrStandby = errors.New("device is in standby")
)
