// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package elbaccesslogs // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension/internal/unmarshaler/elb-access-log"

import "strings"

// arnRegionAccount returns the region and the 12-digit account of an ARN, or two empty strings when the value is
// not an ARN that holds both ("-" and the empty string included).
func arnRegionAccount(arn string) (region, account string) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" || parts[3] == "" || len(parts[4]) != 12 {
		return "", ""
	}
	for _, c := range parts[4] {
		if c < '0' || c > '9' {
			return "", ""
		}
	}
	return parts[3], parts[4]
}

// noteARN keeps the region and account of the first ARN that holds both. One object holds the lines of one load
// balancer, so the first is the one for the whole resource.
func (r *resourceAttributes) noteARN(arn string) {
	if r.region != "" {
		return
	}
	r.region, r.account = arnRegionAccount(arn)
}
