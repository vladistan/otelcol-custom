// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package main

import "errors"

func allocBuffer(int) ([]byte, error) { return nil, errors.New("the capture tool runs on Linux only") }

func freeBuffer([]byte) {}
