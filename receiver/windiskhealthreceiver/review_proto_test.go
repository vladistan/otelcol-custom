// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windiskhealthreceiver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// An ATA request passes only when it is, byte for byte, what ataRequest builds.
func TestCheckRequest_EveryByteOfAnATARequestIsPinned(t *testing.T) {
	base := ataRequest(smartdecode.SmartReadDataCommand(), 5)
	require.NoError(t, checkRequest(base))

	// The bytes that no builder sets: reserved fields, the previous task file and the reserved task file byte.
	var unset []int
	for i := 4; i < 8; i++ { // path, target, lun, reserved
		unset = append(unset, i)
	}
	for i := 16; i < 24; i++ { // reserved and padding before the data offset
		unset = append(unset, i)
	}
	for i := 32; i < 40; i++ { // previous task file
		unset = append(unset, i)
	}
	unset = append(unset, ataTaskFile+7) // reserved task file byte
	for _, off := range unset {
		in := append([]byte(nil), base.in...)
		in[off] = 0x01
		err := checkRequest(request{code: base.code, in: in, outLen: base.outLen})
		require.Error(t, err, "byte %d", off)
		assert.ErrorIs(t, err, smartdecode.ErrCommandNotAllowed, "byte %d", off)
	}

	for _, secs := range []uint32{0, 3601, 1 << 31} {
		r := ataRequest(smartdecode.SmartReadDataCommand(), secs)
		assert.ErrorIs(t, checkRequest(r), smartdecode.ErrCommandNotAllowed, fmt.Sprintf("timeout of %d s", secs))
	}
	for _, secs := range []uint32{1, 5, 3600} {
		assert.NoError(t, checkRequest(ataRequest(smartdecode.SmartReadDataCommand(), secs)), fmt.Sprintf("timeout of %d s", secs))
	}
}
