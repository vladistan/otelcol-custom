// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

// cspell:ignore BSER CSER

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// structure builds one SMBIOS structure: the 4-byte header, the formatted
// bytes after it and the string set.
func structure(typ byte, formatted []byte, strs ...string) []byte {
	out := []byte{typ, byte(4 + len(formatted)), 0x10, 0x00}
	out = append(out, formatted...)
	if len(strs) == 0 {
		return append(out, 0, 0)
	}
	for _, s := range strs {
		out = append(out, s...)
		out = append(out, 0)
	}
	return append(out, 0)
}

type dimmSpec struct {
	size            uint16
	ext             uint32
	memType, form   byte
	speed           uint16
	extSpeed        uint32
	configured      uint16
	extConfigured   uint32
	locator, bank   byte
	mfr, sn, asset  byte
	part            byte
	short           bool // stop the formatted area before the size field
	noExt, noExtSpd bool
}

// dimmStructure builds a type 17 structure with the fields at their SMBIOS offsets.
func dimmStructure(d dimmSpec, strs ...string) []byte {
	n := 0x54 + 4 + 4 // through the extended speeds
	if d.noExtSpd {
		n = 0x20
	}
	if d.noExt {
		n = 0x1C
	}
	if d.short {
		n = 0x0C
	}
	f := make([]byte, n-4)
	set16 := func(off int, v uint16) {
		if off-4+2 <= len(f) {
			binary.LittleEndian.PutUint16(f[off-4:], v)
		}
	}
	set8 := func(off int, v byte) {
		if off-4 < len(f) {
			f[off-4] = v
		}
	}
	set32 := func(off int, v uint32) {
		if off-4+4 <= len(f) {
			binary.LittleEndian.PutUint32(f[off-4:], v)
		}
	}
	set16(0x0C, d.size)
	set8(0x0E, d.form)
	set8(0x10, d.locator)
	set8(0x11, d.bank)
	set8(0x12, d.memType)
	set16(0x15, d.speed)
	set8(0x17, d.mfr)
	set8(0x18, d.sn)
	set8(0x19, d.asset)
	set8(0x1A, d.part)
	set32(0x1C, d.ext)
	set16(0x20, d.configured)
	set32(0x54, d.extSpeed)
	set32(0x58, d.extConfigured)
	return structure(17, f, strs...)
}

func endMarker() []byte { return structure(127, nil) }

func joinTable(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func goodDimm() []byte {
	return dimmStructure(dimmSpec{size: 16384, memType: 0x1A, form: 0x09, speed: 3200, locator: 1, bank: 2, mfr: 3, sn: 4, asset: 5, part: 6},
		"DIMM_A1", "BANK 0", "Samsung", "SN-TEST-0001", "Not Specified", "M393A2K43DB3")
}

func TestParseSMBIOSMemoryModule(t *testing.T) {
	info := parseSMBIOS(joinTable(goodDimm(), endMarker()))

	require.Len(t, info.modules, 1)
	m := info.modules[0]
	assert.Equal(t, "DIMM_A1", m.locator)
	assert.Equal(t, "BANK 0", m.bank)
	assert.Equal(t, "Samsung", m.manufacturer)
	assert.Equal(t, "SN-TEST-0001", m.serial)
	assert.Empty(t, m.asset, "a placeholder is absent")
	assert.Equal(t, "M393A2K43DB3", m.part)
	assert.Equal(t, uint64(16384)<<20, m.size)
	assert.Equal(t, "DDR4", m.memType)
	assert.Equal(t, "DIMM", m.formFactor)
	assert.Equal(t, uint32(3200), m.speed)
}

func TestParseSMBIOSMemorySizes(t *testing.T) {
	tests := map[string]struct {
		spec dimmSpec
		want uint64
	}{
		"megabytes":             {dimmSpec{size: 8192, locator: 1}, 8192 << 20},
		"kilobytes":             {dimmSpec{size: 0x8000 | 512, locator: 1}, 512 << 10},
		"extended":              {dimmSpec{size: 0x7FFF, ext: 65536, locator: 1}, 65536 << 20},
		"extended reserved bit": {dimmSpec{size: 0x7FFF, ext: 0x80000000 | 32768, locator: 1}, 32768 << 20},
		"extended missing":      {dimmSpec{size: 0x7FFF, noExt: true, locator: 1}, 0},
		"unknown size":          {dimmSpec{size: 0xFFFF, locator: 1}, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			info := parseSMBIOS(joinTable(dimmStructure(tt.spec, "DIMM_B2"), endMarker()))

			require.Len(t, info.modules, 1)
			assert.Equal(t, tt.want, info.modules[0].size)
		})
	}
}

func TestParseSMBIOSEmptySlotOrUnreadableSizeGivesNoModule(t *testing.T) {
	tests := map[string]dimmSpec{
		"empty slot":        {size: 0, locator: 1},
		"size not readable": {short: true, locator: 1},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			info := parseSMBIOS(joinTable(dimmStructure(spec, "DIMM_B2"), endMarker()))

			assert.Empty(t, info.modules)
		})
	}
}

func TestParseSMBIOSExtendedSpeed(t *testing.T) {
	info := parseSMBIOS(joinTable(dimmStructure(dimmSpec{size: 8192, speed: 0xFFFF, extSpeed: 6400, locator: 1}, "DIMM_C1"), endMarker()))

	require.Len(t, info.modules, 1)
	assert.Equal(t, uint32(6400), info.modules[0].speed)
}

func TestParseSMBIOSStringIndexBeyondTheSetIsAbsent(t *testing.T) {
	info := parseSMBIOS(joinTable(dimmStructure(dimmSpec{size: 4096, locator: 1, mfr: 9, part: 200}, "DIMM_D1"), endMarker()))

	require.Len(t, info.modules, 1)
	assert.Equal(t, "DIMM_D1", info.modules[0].locator)
	assert.Empty(t, info.modules[0].manufacturer)
	assert.Empty(t, info.modules[0].part)
}

func TestParseSMBIOSSystemBoardBiosChassis(t *testing.T) {
	table := joinTable(
		structure(0, []byte{1, 2, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Vendor Test", "1.2.3", "04/05/2024"),
		structure(1, []byte{1, 2, 3, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Mfr One", "Prod Two", "v3", "SER-4"),
		structure(2, []byte{1, 2, 3, 4, 5}, "Board Mfr", "Board Name", "r1", "BSER", "ASSET1"),
		structure(3, []byte{1, 0x17, 2, 3, 4}, "Chassis Mfr", "v", "CSER", "CASSET"),
		endMarker(),
	)

	d := parseSMBIOS(table).dmi

	assert.Equal(t, "Vendor Test", d.biosVendor)
	assert.Equal(t, "1.2.3", d.biosVersion)
	assert.Equal(t, "04/05/2024", d.biosDate)
	assert.Equal(t, "Mfr One", d.sysVendor)
	assert.Equal(t, "Prod Two", d.productName)
	assert.Equal(t, "v3", d.productVersion)
	assert.Equal(t, "SER-4", d.productSerial)
	assert.Equal(t, "Board Mfr", d.boardVendor)
	assert.Equal(t, "Board Name", d.boardName)
	assert.Equal(t, "BSER", d.boardSerial)
	assert.Equal(t, "ASSET1", d.boardAsset)
	assert.Equal(t, "Chassis Mfr", d.chassisVendor)
	assert.Equal(t, "Rack Mount Chassis", d.chassisType)
	assert.Equal(t, "CSER", d.chassisSerial)
}

func TestParseSMBIOSStopsAtTheEndMarker(t *testing.T) {
	info := parseSMBIOS(joinTable(goodDimm(), endMarker(), goodDimm()))

	assert.Len(t, info.modules, 1)
}

func TestParseSMBIOSMalformedTablesDoNotPanicAndKeepWhatIsGood(t *testing.T) {
	good := goodDimm()
	tests := map[string][]byte{
		"empty":                   nil,
		"one byte":                {17},
		"header only":             {17, 4},
		"length below the header": {17, 2, 0, 0, 0, 0},
		"length beyond the table": {17, 0xFF, 0, 0, 1, 2, 3},
		"truncated structure":     good[:20],
		"unterminated strings":    append(append([]byte{}, good[:0x28]...), []byte("DIMM")...),
		"zero length strings":     {17, 4, 0, 0},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() { parseSMBIOS(data) })
		})
	}

	info := parseSMBIOS(joinTable(good, []byte{17, 0xFF, 0, 0, 1}))
	assert.Len(t, info.modules, 1, "a structure before the damage is kept")
}

func TestParseSMBIOSCapsTheModuleCount(t *testing.T) {
	var parts [][]byte
	for i := 0; i < maxModules+50; i++ {
		parts = append(parts, goodDimm())
	}

	info := parseSMBIOS(joinTable(parts...))

	assert.Len(t, info.modules, maxModules)
}

func TestParseSMBIOSRandomBytesDoNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		data := make([]byte, rng.Intn(300))
		rng.Read(data)
		assert.NotPanics(t, func() { parseSMBIOS(data) })
	}
}

func TestParseSMBIOSRandomMutationsOfAGoodTableDoNotPanic(t *testing.T) {
	base := joinTable(goodDimm(), goodDimm(), endMarker())
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		data := append([]byte{}, base...)
		for j := 0; j < 1+rng.Intn(6); j++ {
			data[rng.Intn(len(data))] = byte(rng.Intn(256))
		}
		data = data[:rng.Intn(len(data)+1)]
		assert.NotPanics(t, func() { parseSMBIOS(data) })
	}
}

func FuzzParseSMBIOS(f *testing.F) {
	f.Add(joinTable(goodDimm(), endMarker()))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		info := parseSMBIOS(data)
		if len(info.modules) > maxModules {
			t.Fatal("module cap exceeded")
		}
	})
}

func TestParseSMBIOSConfiguredSpeedByVersion(t *testing.T) {
	ddr4 := func(speed, configured uint16) dimmSpec {
		return dimmSpec{size: 8192, memType: 0x1A, speed: speed, configured: configured, locator: 1}
	}
	tests := map[string]struct {
		ver  smbiosVersion
		spec dimmSpec
		want uint32
	}{
		"2.8 clock in MHz is doubled":            {smbiosVersion{2, 8}, ddr4(2400, 1200), 2400},
		"3.0 clock in MHz is doubled":            {smbiosVersion{3, 0}, ddr4(2666, 1333), 2666},
		"3.1 value is MT/s and is kept":          {smbiosVersion{3, 1}, ddr4(2666, 1333), 1333},
		"3.0 plausible MT/s 2400 is kept":        {smbiosVersion{3, 0}, ddr4(2400, 2400), 2400},
		"3.0 plausible MT/s 2133 is kept":        {smbiosVersion{3, 0}, ddr4(2133, 2133), 2133},
		"2.7 DDR4 1600 is the floor and is kept": {smbiosVersion{2, 7}, ddr4(1600, 1600), 1600},
		"2.8 already in MT/s is kept":            {smbiosVersion{2, 8}, ddr4(2400, 2400), 2400},
		"3.2 value is MT/s":                      {smbiosVersion{3, 2}, ddr4(2400, 2400), 2400},
		"3.3 small value is not doubled":         {smbiosVersion{3, 3}, ddr4(3200, 1200), 1200},
		"unknown version is kept":                {smbiosVersion{}, ddr4(2400, 1200), 1200},
		"2.8 unknown type is kept":               {smbiosVersion{2, 8}, dimmSpec{size: 8192, speed: 2400, configured: 1200, locator: 1}, 1200},
		"3.3 extended configured speed":          {smbiosVersion{3, 3}, dimmSpec{size: 8192, memType: 0x22, speed: 0xFFFF, extSpeed: 8800, configured: 0xFFFF, extConfigured: 0x80000000 | 8400, locator: 1}, 8400},
		"3.3 extended configured needs the flag": {smbiosVersion{3, 3}, dimmSpec{size: 8192, memType: 0x22, speed: 4800, configured: 4800, extConfigured: 7000, locator: 1}, 4800},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			info := parseSMBIOSVersion(joinTable(dimmStructure(tt.spec, "DIMM_E1"), endMarker()), tt.ver)

			require.Len(t, info.modules, 1)
			assert.Equal(t, tt.want, info.modules[0].configured)
		})
	}
}

func TestParseSMBIOSConfiguredSpeedFloorPerType(t *testing.T) {
	types := map[string]struct {
		memType byte
		floor   uint16
	}{
		"DDR":          {0x12, 200},
		"DDR2":         {0x13, 400},
		"DDR2 FB-DIMM": {0x14, 400},
		"DDR3":         {0x18, 800},
		"DDR4":         {0x1A, 1600},
	}
	for name, ty := range types {
		spec := func(configured uint16) dimmSpec {
			return dimmSpec{size: 8192, memType: ty.memType, speed: ty.floor * 2, configured: configured, locator: 1}
		}
		read := func(ver smbiosVersion, configured uint16) uint32 {
			info := parseSMBIOSVersion(joinTable(dimmStructure(spec(configured), "DIMM_A1"), endMarker()), ver)
			require.Len(t, info.modules, 1)
			return info.modules[0].configured
		}
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, uint32(ty.floor), read(smbiosVersion{3, 0}, ty.floor/2), "a clock under the floor is doubled before 3.1")
			assert.Equal(t, uint32(ty.floor/2), read(smbiosVersion{3, 1}, ty.floor/2), "3.1 is MT/s")
			assert.Equal(t, uint32(ty.floor), read(smbiosVersion{3, 0}, ty.floor), "a value at the floor is kept")
			assert.Equal(t, uint32(ty.floor+1), read(smbiosVersion{2, 8}, ty.floor+1), "a value over the floor is kept")
		})
	}
}

func TestParseSMBIOSExtendedSpeedClearsTheReservedBit(t *testing.T) {
	spec := dimmSpec{size: 8192, speed: 0xFFFF, extSpeed: 0x80000000 | 6400, locator: 1}

	info := parseSMBIOSVersion(joinTable(dimmStructure(spec, "DIMM_C2"), endMarker()), smbiosVersion{3, 3})

	require.Len(t, info.modules, 1)
	assert.Equal(t, uint32(6400), info.modules[0].speed)
}

func TestEntryPointVersion(t *testing.T) {
	sm3 := append([]byte("_SM3_"), 0, 0x18, 3, 6, 0, 1)
	sm2 := append([]byte("_SM_"), 0, 0x1F, 2, 8)

	assert.Equal(t, smbiosVersion{3, 6}, entryPointVersion(sm3))
	assert.Equal(t, smbiosVersion{2, 8}, entryPointVersion(sm2))
	assert.Equal(t, smbiosVersion{}, entryPointVersion([]byte("_SM3_")), "too short")
	assert.Equal(t, smbiosVersion{}, entryPointVersion([]byte("garbage-bytes")))
	assert.Equal(t, smbiosVersion{}, entryPointVersion(nil))
}

func TestRawVersionReadsTheWindowsBufferHeader(t *testing.T) {
	assert.Equal(t, smbiosVersion{3, 2}, rawVersion([]byte{0, 3, 2, 0, 0, 0, 0, 0}))
	assert.Equal(t, smbiosVersion{}, rawVersion([]byte{0, 3}))
}

func TestSMBIOSVersionAtLeast(t *testing.T) {
	assert.True(t, smbiosVersion{3, 2}.atLeast(3, 2))
	assert.True(t, smbiosVersion{4, 0}.atLeast(3, 3))
	assert.False(t, smbiosVersion{3, 1}.atLeast(3, 2))
	assert.False(t, smbiosVersion{}.atLeast(3, 2), "an unknown version is not at least anything")
}

func TestStripRawSMBIOSHeader(t *testing.T) {
	table := joinTable(goodDimm(), endMarker())
	raw := append([]byte{0, 3, 2, 0, 0, 0, 0, 0}, table...)
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table)))

	got, ok := stripRawSMBIOS(raw)

	require.True(t, ok)
	assert.Equal(t, table, got)

	_, ok = stripRawSMBIOS(raw[:6])
	assert.False(t, ok)
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table))+10)
	_, ok = stripRawSMBIOS(raw)
	assert.False(t, ok, "a length beyond the buffer is refused")
}
