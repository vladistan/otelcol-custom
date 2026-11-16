// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package megaraidreceiver

import "errors"

// allocBuffer is not available off Unix; the receiver does not run there.
func allocBuffer(int) ([]byte, error) {
	return nil, errors.New("megaraid buffers need a Unix host")
}

func freeBuffer([]byte) {}
