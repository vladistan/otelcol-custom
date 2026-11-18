// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import "encoding/binary"

const (
	maxTableBytes = 1 << 20
	maxStructures = 8192
	maxModules    = 512
	maxStrings    = 256
	maxStringLen  = 256
)

// dmiFacts are the text fields of the system, board, BIOS and chassis records.
type dmiFacts struct {
	sysVendor, productName, productVersion, productSerial, productFamily string
	boardVendor, boardName, boardVersion, boardSerial, boardAsset        string
	biosVendor, biosVersion, biosDate                                    string
	chassisVendor, chassisType, chassisSerial, chassisAsset              string
}

// memModule is one populated SMBIOS memory device.
type memModule struct {
	handle                                           uint16
	locator, bank, manufacturer, serial, asset, part string
	memType, formFactor                              string
	size                                             uint64
	speed, configured                                uint32
}

type smbiosInfo struct {
	dmi     dmiFacts
	modules []memModule
}

// stripRawSMBIOS removes the 8-byte header of the Windows firmware table buffer.
func stripRawSMBIOS(raw []byte) ([]byte, bool) {
	if len(raw) < 8 {
		return nil, false
	}
	n := binary.LittleEndian.Uint32(raw[4:])
	if uint64(n) > uint64(len(raw)-8) {
		return nil, false
	}
	return raw[8 : 8+n], true
}

// smbiosVersion is the SMBIOS specification version of the table; the zero
// value means it is not known.
type smbiosVersion struct{ major, minor int }

// atLeast tells whether the version is known and not older than major.minor.
func (v smbiosVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

// entryPointVersion reads the version from an SMBIOS 2.x ("_SM_") or 3.x
// ("_SM3_") entry point; anything else gives the zero version.
func entryPointVersion(raw []byte) smbiosVersion {
	switch {
	case len(raw) >= 9 && string(raw[:5]) == "_SM3_":
		return smbiosVersion{int(raw[7]), int(raw[8])}
	case len(raw) >= 8 && string(raw[:4]) == "_SM_":
		return smbiosVersion{int(raw[6]), int(raw[7])}
	}
	return smbiosVersion{}
}

// rawVersion reads the version from the 8-byte header of the Windows firmware table buffer.
func rawVersion(raw []byte) smbiosVersion {
	if len(raw) < 3 {
		return smbiosVersion{}
	}
	return smbiosVersion{int(raw[1]), int(raw[2])}
}

// minDDRSpeed is the lowest data rate in MT/s a module of the memory type can run at. Before
// SMBIOS 3.1 the configured speed may be a clock in MHz, half the data rate. Doubling is a
// heuristic: a value under the floor of its type is doubled, any other value is kept.
var minDDRSpeed = map[byte]uint32{0x12: 200, 0x13: 400, 0x14: 400, 0x18: 800, 0x1A: 1600}

// parseSMBIOS walks the structure table of an unknown version.
func parseSMBIOS(data []byte) smbiosInfo { return parseSMBIOSVersion(data, smbiosVersion{}) }

// parseSMBIOSVersion walks the structure table. Every read is bounds-checked: a
// damaged table gives the structures before the damage.
func parseSMBIOSVersion(data []byte, ver smbiosVersion) smbiosInfo {
	var info smbiosInfo
	if len(data) > maxTableBytes {
		data = data[:maxTableBytes]
	}
	seen := map[byte]bool{}
	off := 0
	for n := 0; n < maxStructures && off+4 <= len(data); n++ {
		typ, length := data[off], int(data[off+1])
		if length < 4 || off+length > len(data) {
			break
		}
		formatted := data[off : off+length]
		strs, next, complete := readStrings(data, off+length)
		switch typ {
		case 0, 1, 2, 3:
			if !seen[typ] {
				seen[typ] = true
				fillDMI(&info.dmi, typ, formatted, strs)
			}
		case 17:
			if len(info.modules) < maxModules {
				if m, ok := parseMemoryDevice(formatted, strs, ver); ok {
					info.modules = append(info.modules, m)
				}
			}
		case 127:
			return info
		}
		if !complete {
			break
		}
		off = next
	}
	return info
}

// readStrings reads the string set after a structure. complete is false when the set runs past the data.
func readStrings(data []byte, off int) (strs []string, next int, complete bool) {
	if off+2 <= len(data) && data[off] == 0 && data[off+1] == 0 {
		return nil, off + 2, true
	}
	for len(strs) < maxStrings {
		if off >= len(data) {
			return strs, off, false
		}
		end := off
		for end < len(data) && data[end] != 0 {
			end++
		}
		if end >= len(data) {
			return strs, end, false
		}
		if end == off {
			return strs, end + 1, true
		}
		s := data[off:end]
		if len(s) > maxStringLen {
			s = s[:maxStringLen]
		}
		strs = append(strs, string(s))
		off = end + 1
	}
	return strs, off, false
}

func strAt(strs []string, idx byte) string {
	if idx == 0 || int(idx) > len(strs) {
		return ""
	}
	return cleanValue(strs[idx-1])
}

func u8(b []byte, off int) (byte, bool) {
	if off >= len(b) {
		return 0, false
	}
	return b[off], true
}

func u16(b []byte, off int) (uint16, bool) {
	if off+2 > len(b) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(b[off:]), true
}

func u32(b []byte, off int) (uint32, bool) {
	if off+4 > len(b) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[off:]), true
}

func strField(b []byte, strs []string, off int) string {
	idx, ok := u8(b, off)
	if !ok {
		return ""
	}
	return strAt(strs, idx)
}

func fillDMI(d *dmiFacts, typ byte, b []byte, strs []string) {
	switch typ {
	case 0:
		d.biosVendor, d.biosVersion, d.biosDate = strField(b, strs, 4), strField(b, strs, 5), strField(b, strs, 8)
	case 1:
		d.sysVendor, d.productName = strField(b, strs, 4), strField(b, strs, 5)
		d.productVersion, d.productSerial = strField(b, strs, 6), strField(b, strs, 7)
		d.productFamily = strField(b, strs, 0x1A)
	case 2:
		d.boardVendor, d.boardName = strField(b, strs, 4), strField(b, strs, 5)
		d.boardVersion, d.boardSerial, d.boardAsset = strField(b, strs, 6), strField(b, strs, 7), strField(b, strs, 8)
	case 3:
		d.chassisVendor = strField(b, strs, 4)
		if t, ok := u8(b, 5); ok {
			d.chassisType = chassisTypes[t&0x7F]
		}
		d.chassisSerial, d.chassisAsset = strField(b, strs, 7), strField(b, strs, 8)
	}
}

var chassisTypes = map[byte]string{
	0x03: "Desktop", 0x04: "Low Profile Desktop", 0x06: "Mini Tower", 0x07: "Tower", 0x08: "Portable", 0x09: "Laptop",
	0x0A: "Notebook", 0x0D: "All in One", 0x11: "Main Server Chassis", 0x17: "Rack Mount Chassis", 0x18: "Sealed-case PC",
	0x1C: "Blade", 0x1D: "Blade Enclosure", 0x1E: "Tablet", 0x1F: "Convertible", 0x22: "Embedded PC", 0x23: "Mini PC",
}

var memoryTypes = map[byte]string{
	0x12: "DDR", 0x13: "DDR2", 0x14: "DDR2 FB-DIMM", 0x18: "DDR3", 0x1A: "DDR4", 0x1B: "LPDDR", 0x1C: "LPDDR2",
	0x1D: "LPDDR3", 0x1E: "LPDDR4", 0x20: "HBM", 0x21: "HBM2", 0x22: "DDR5", 0x23: "LPDDR5", 0x24: "HBM3",
}

var formFactors = map[byte]string{0x09: "DIMM", 0x0C: "RIMM", 0x0D: "SODIMM", 0x0F: "FB-DIMM", 0x10: "Die"}

// parseMemoryDevice reads a type 17 structure; an empty slot or an unreadable size gives false.
func parseMemoryDevice(b []byte, strs []string, ver smbiosVersion) (memModule, bool) {
	raw, ok := u16(b, 0x0C)
	if !ok || raw == 0 {
		return memModule{}, false
	}
	m := memModule{}
	m.handle, _ = u16(b, 2)
	switch {
	case raw == 0xFFFF:
	case raw == 0x7FFF:
		if ext, ok := u32(b, 0x1C); ok {
			m.size = uint64(ext&0x7FFFFFFF) << 20
		}
	case raw&0x8000 != 0:
		m.size = uint64(raw&0x7FFF) << 10
	default:
		m.size = uint64(raw) << 20
	}
	if f, ok := u8(b, 0x0E); ok {
		m.formFactor = formFactors[f]
	}
	m.locator, m.bank = strField(b, strs, 0x10), strField(b, strs, 0x11)
	if t, ok := u8(b, 0x12); ok {
		m.memType = memoryTypes[t]
	}
	// Speeds above 65534 MT/s sit in a DWORD (SMBIOS 3.3: 0x54 speed, 0x58 configured speed); bit 31 is reserved.
	if s, ok := u16(b, 0x15); ok {
		m.speed = uint32(s)
		if s == 0xFFFF {
			ext, _ := u32(b, 0x54)
			m.speed = ext & 0x7FFFFFFF
		}
	}
	if s, ok := u16(b, 0x20); ok {
		switch {
		case s == 0xFFFF:
			ext, _ := u32(b, 0x58)
			m.configured = ext & 0x7FFFFFFF
		case s != 0:
			m.configured = uint32(s)
			t, _ := u8(b, 0x12)
			if floor := minDDRSpeed[t]; ver.major > 0 && !ver.atLeast(3, 1) && floor > 0 && m.configured < floor {
				m.configured *= 2
			}
		}
	}
	m.manufacturer, m.serial = strField(b, strs, 0x17), strField(b, strs, 0x18)
	m.asset, m.part = strField(b, strs, 0x19), strField(b, strs, 0x1A)
	return m, true
}
