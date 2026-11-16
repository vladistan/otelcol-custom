// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"encoding/base64"
	"strings"
)

// base64Lines wraps DER bytes in base64 lines of 64 characters for a PEM block.
func base64Lines(der []byte) string {
	enc := base64.StdEncoding.EncodeToString(der)
	var b strings.Builder
	for len(enc) > 64 {
		b.WriteString(enc[:64] + "\n")
		enc = enc[64:]
	}
	b.WriteString(enc + "\n")
	return b.String()
}
