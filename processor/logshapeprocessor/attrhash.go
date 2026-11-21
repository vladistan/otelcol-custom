// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"hash/maphash"
	"math"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

var hashSeed = maphash.MakeSeed()

// The grouping calls the hashes through these variables so a test can force every hash to collide.
var (
	hashResource = attrsHash
	hashScopeOf  = scopeHash
)

// attrsHash hashes an attribute map without regard to the order of its keys. Equal maps give equal
// hashes; a caller that groups by the hash confirms with mapsEqual.
func attrsHash(m pcommon.Map) uint64 {
	var sum uint64
	m.Range(func(k string, v pcommon.Value) bool {
		sum += maphash.String(hashSeed, k)*31 + valueHash(v)
		return true
	})
	return sum
}

func valueHash(v pcommon.Value) uint64 {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		return maphash.String(hashSeed, v.Str())
	case pcommon.ValueTypeInt:
		return uint64(v.Int())
	case pcommon.ValueTypeDouble:
		return math.Float64bits(v.Double())
	case pcommon.ValueTypeBool:
		if v.Bool() {
			return 1
		}
		return 2
	case pcommon.ValueTypeMap:
		return attrsHash(v.Map())
	case pcommon.ValueTypeBytes:
		return maphash.Bytes(hashSeed, v.Bytes().AsRaw())
	}
	return uint64(v.Type())
}

// scopeHash hashes what scopesEqual compares.
func scopeHash(s pcommon.InstrumentationScope) uint64 {
	return maphash.String(hashSeed, s.Name())*17 + maphash.String(hashSeed, s.Version())*13 + attrsHash(s.Attributes())
}
