// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// defaultGCPLBErrorValues are the load balancer statusDetails values that mark a request as an error.
var defaultGCPLBErrorValues = []string{"unsupported_method", "invalid_http2_request_content_length"}

// gcpLBErrorValues returns the code defaults followed by the configured values, without duplicates.
func gcpLBErrorValues(extra []string) []string {
	out := append([]string(nil), defaultGCPLBErrorValues...)
	for _, v := range extra {
		dup := false
		for _, o := range out {
			dup = dup || o == v
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out
}
