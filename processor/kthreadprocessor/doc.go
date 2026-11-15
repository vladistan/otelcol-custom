// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package kthreadprocessor replaces the per-process resources of kernel threads
// in process metrics with one aggregate resource per scrape.
//
// A resource whose process.executable.name matches a configured pattern is
// merged into the aggregate; every metric is summed over the merged resources,
// point by point on equal attributes. All other resources pass unchanged.
package kthreadprocessor // import "github.com/vladistan/otelcol-custom/processor/kthreadprocessor"
