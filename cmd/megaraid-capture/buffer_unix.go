// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package main

import (
	"fmt"
	"syscall"
)

// allocBuffer returns anonymous memory the Go collector neither moves nor frees. The driver
// receives the address inside the ioctl packet as a raw pointer, so the buffer must stay put.
func allocBuffer(n int) ([]byte, error) {
	b, err := syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("allocate a %d byte buffer: %w", n, err)
	}
	return b, nil
}

func freeBuffer(b []byte) { _ = syscall.Munmap(b) }
