// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	winCPUKey   = `HARDWARE\DESCRIPTION\System\CentralProcessor`
	winBIOSKey  = `HARDWARE\DESCRIPTION\System\BIOS`
	winGPUKey   = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	maxWinDrive = 128
)

// ctlCode builds a Windows device control code (CTL_CODE of winioctl.h).
func ctlCode(deviceType, function, method, access uint32) uint32 {
	return deviceType<<16 | access<<14 | function<<2 | method
}

const (
	fileAnyAccess  = 0
	fileReadAccess = 1
	methodBuffered = 0
	genericRead    = 0x80000000

	// IOCTL_STORAGE_QUERY_PROPERTY needs no access; IOCTL_DISK_GET_LENGTH_INFO needs read access.
	ioctlStorageQuery   = 0x2D1400 // ctlCode(0x2D, 0x500, methodBuffered, fileAnyAccess)
	ioctlDiskLengthInfo = 0x7405C  // ctlCode(7, 0x17, methodBuffered, fileReadAccess)

	// driveOpenAccess is the access the drive handle is opened with: the length query needs it.
	driveOpenAccess = genericRead
)

const noteDriveSize = "drive size is not readable"

const (
	busTypeVirtual       = 14
	busTypeFileBacked    = 15
	busTypeStorageSpaces = 16
)

// errWinNotFound means the key, value or device does not exist.
var errWinNotFound = errors.New("not found")

type winDrive struct {
	descriptor      []byte
	size            uint64
	rotational      bool
	rotationalKnown bool
}

// winAPI is what the Windows source needs from the system. The real
// implementation is in winapi_windows.go; tests inject a fake.
type winAPI interface {
	firmwareTable() ([]byte, error)
	registryString(key, name string) (string, error)
	registryNumber(key, name string) (uint64, error)
	registrySubKeys(key string) ([]string, error)
	processorInfo() ([]byte, error)
	drive(index int) (winDrive, error)
	// physicalMemory gives the total physical memory in bytes.
	physicalMemory() (uint64, error)
}

type windowsSource struct {
	api       winAPI
	maxDrives int
}

func (s *windowsSource) read(ctx context.Context) inventory {
	inv := newInventory()
	var smbios smbiosInfo
	tableOK := false
	if raw, err := s.api.firmwareTable(); err == nil {
		if table, ok := stripRawSMBIOS(raw); ok {
			smbios, tableOK = parseSMBIOSVersion(table, rawVersion(raw)), true
		}
	}
	dmi := smbios.dmi
	dmiOK := tableOK
	if !tableOK {
		inv.dmiUnreadable = true
		dmi, dmiOK = s.registryDMI()
	}

	if virtual, why := virtualFromDMI(dmi.sysVendor, dmi.productName, dmi.boardVendor, dmi.biosVendor); virtual {
		inv.host, inv.hostReason = hostVirtual, why
		return inv
	}
	if !dmiOK {
		inv.host, inv.hostReason = hostUndecided, "no firmware data to decide from"
	}

	s.addCPU(&inv)
	inv.status[srcBoard], inv.status[srcBIOS], inv.status[srcSystem] = dmiOK, dmiOK, dmiOK
	if dmiOK {
		dmi.addFacts(&inv)
	}
	inv.status[srcMemory] = tableOK
	for _, m := range smbios.modules {
		inv.facts = append(inv.facts, memoryFact(m))
	}
	s.addMemoryTotal(&inv)
	if ctx.Err() != nil {
		return inv
	}
	s.addDrives(ctx, &inv)
	s.addGPUs(&inv)
	return inv
}

// addMemoryTotal adds the physical RAM the system reports; a zero or a value above int64 gives none.
func (s *windowsSource) addMemoryTotal(inv *inventory) {
	total, err := s.api.physicalMemory()
	ok := err == nil && total > 0 && total <= math.MaxInt64
	inv.status[srcMemoryTotal] = ok
	if ok {
		inv.facts = append(inv.facts, memoryTotalFact(int64(total)))
	}
}

// registryDMI reads the firmware strings Windows copies to the registry; it has no serial numbers.
func (s *windowsSource) registryDMI() (dmiFacts, bool) {
	var found bool
	get := func(name string) string {
		v, err := s.api.registryString(winBIOSKey, name)
		if err != nil {
			return ""
		}
		v = cleanValue(v)
		found = found || v != ""
		return v
	}
	d := dmiFacts{
		sysVendor: get("SystemManufacturer"), productName: get("SystemProductName"), productVersion: get("SystemVersion"),
		productFamily: get("SystemFamily"), boardVendor: get("BaseBoardManufacturer"), boardName: get("BaseBoardProduct"),
		boardVersion: get("BaseBoardVersion"), biosVendor: get("BIOSVendor"), biosVersion: get("BIOSVersion"),
		biosDate: get("BIOSReleaseDate"),
	}
	return d, found
}

func (s *windowsSource) addCPU(inv *inventory) {
	var model, vendor string
	var mhz uint64
	if v, err := s.api.registryString(winCPUKey+`\0`, "ProcessorNameString"); err == nil {
		model = v
	}
	if v, err := s.api.registryString(winCPUKey+`\0`, "VendorIdentifier"); err == nil {
		vendor = v
	}
	if v, err := s.api.registryNumber(winCPUKey+`\0`, "~MHz"); err == nil && v < 1_000_000 {
		mhz = v
	}
	logical := 0
	if subs, err := s.api.registrySubKeys(winCPUKey); err == nil {
		logical = len(subs)
	}
	var sockets []socketCounts
	if buf, err := s.api.processorInfo(); err == nil {
		sockets, _ = parseProcessorInfo(buf)
	}
	inv.status[srcCPU] = cleanValue(model) != "" || len(sockets) > 0
	if !inv.status[srcCPU] {
		return
	}
	build := func(id int, c socketCounts, multi bool) fact {
		f := newFact(metricCPU)
		f.attrs[attrID] = "cpu/" + strconv.Itoa(id)
		f.setStr(attrVendor, vendor)
		f.setStr(attrModel, model)
		if multi {
			f.attrs[attrCPUSocket] = int64(id)
			f.setNum(attrCPUSockets, int64(len(sockets)))
		}
		f.setNum(attrCPUCores, int64(c.cores))
		f.setNum(attrCPUThreads, int64(c.threads))
		f.setNum(attrCPUNominal, int64(mhz)*1_000_000)
		return f
	}
	if len(sockets) == 0 {
		inv.facts = append(inv.facts, build(0, socketCounts{threads: logical}, false))
		return
	}
	for i, c := range sockets {
		inv.facts = append(inv.facts, build(i, c, true))
	}
}

func (s *windowsSource) addDrives(ctx context.Context, inv *inventory) {
	failed, count := false, 0
	for i := 0; i < maxWinDrive; i++ {
		if ctx.Err() != nil {
			return
		}
		d, err := s.api.drive(i)
		if errors.Is(err, errWinNotFound) {
			continue
		}
		if err != nil {
			failed = true
			continue
		}
		f, ok := winDriveFact(d)
		if !ok {
			continue
		}
		if _, sized := f.attrs[attrDriveSize]; !sized && !slices.Contains(inv.notes, noteDriveSize) {
			inv.notes = append(inv.notes, noteDriveSize)
		}
		if count >= s.maxDrives {
			inv.drivesCapped = true
			break
		}
		count++
		inv.facts = append(inv.facts, f)
	}
	inv.status[srcDrive] = !failed
}

var winBuses = map[uint32]string{
	1: "scsi", 3: "ata", 6: "fc", 7: "usb", 8: "raid", 9: "iscsi", 10: "sas", 11: "sata", 12: "sd", 13: "mmc", 17: "nvme",
}

// winVirtualBuses are the STORAGE_BUS_TYPE values of virtual disks, file-backed disks and storage
// spaces; they are not physical drives.
var winVirtualBuses = map[uint32]bool{
	busTypeVirtual:       true,
	busTypeFileBacked:    true,
	busTypeStorageSpaces: true,
}

func winDriveFact(d winDrive) (fact, bool) {
	desc, ok := parseStorageDescriptor(d.descriptor)
	if !ok || winVirtualBuses[desc.bus] {
		return fact{}, false
	}
	f := newFact(metricDrive)
	if v := cleanValue(desc.vendor); !strings.EqualFold(v, "ATA") && !strings.EqualFold(v, "NVMe") {
		f.setStr(attrVendor, v)
	}
	f.setStr(attrModel, desc.product)
	f.setStr(attrFirmware, desc.revision)
	f.setStr(attrSerial, desc.serial)
	if !f.hasData() {
		return fact{}, false
	}
	if d.size > 0 && d.size < 1<<62 {
		f.setNum(attrDriveSize, int64(d.size))
	}
	if d.rotationalKnown {
		if d.rotational {
			f.attrs[attrDriveType] = "HDD"
		} else {
			f.attrs[attrDriveType] = "SSD"
		}
	}
	if bus, ok := winBuses[desc.bus]; ok {
		f.attrs[attrDriveBus] = bus
	}
	id, _ := f.attrs[attrSerial].(string)
	if id == "" {
		id, _ = f.attrs[attrModel].(string)
	}
	f.attrs[attrID] = id
	return f, true
}

var (
	gpuSubKey = regexp.MustCompile(`^\d{4}$`)
	pciIDs    = regexp.MustCompile(`(?i)ven_([0-9a-f]{4})&dev_([0-9a-f]{4})`)
)

func (s *windowsSource) addGPUs(inv *inventory) {
	subs, err := s.api.registrySubKeys(winGPUKey)
	if err != nil {
		inv.status[srcGPU] = false
		return
	}
	inv.status[srcGPU] = true
	count := 0
	for _, sub := range subs {
		if !gpuSubKey.MatchString(sub) || count >= maxGPUs {
			continue
		}
		key := winGPUKey + `\` + sub
		desc, err := s.api.registryString(key, "DriverDesc")
		desc = cleanValue(desc)
		if err != nil || desc == "" || strings.HasPrefix(strings.ToLower(desc), "microsoft ") {
			continue
		}
		f := newFact(metricGPU)
		f.attrs[attrID] = "display/" + sub
		f.setStr(attrModel, desc)
		if v, err := s.api.registryString(key, "DriverVersion"); err == nil {
			f.setStr(attrDriverVersion, v)
		}
		if v, err := s.api.registryString(key, "MatchingDeviceId"); err == nil {
			if m := pciIDs.FindStringSubmatch(v); m != nil {
				f.attrs[attrGPUVendorID] = strings.ToLower(m[1])
				f.attrs[attrGPUDeviceID] = strings.ToLower(m[2])
				vendor, _ := strconv.ParseUint(m[1], 16, 32)
				device, _ := strconv.ParseUint(m[2], 16, 32)
				if isBMCVideo(vendor, device, nil) {
					continue
				}
			}
		}
		if n, err := s.api.registryNumber(key, "HardwareInformation.qwMemorySize"); err == nil && n < 1<<50 {
			f.setNum(attrGPUMemory, int64(n))
		}
		count++
		inv.facts = append(inv.facts, f)
	}
}

type socketCounts struct{ cores, threads int }

type groupMask struct {
	mask  uint64
	group uint16
}

const (
	relProcessorCore    = 0
	relProcessorPackage = 3
	maxProcRecords      = 4096
)

// parseProcessorInfo reads the buffer of GetLogicalProcessorInformationEx
// (all relationships, 64-bit masks) into the counts of each package.
func parseProcessorInfo(buf []byte) ([]socketCounts, bool) {
	var packages, cores [][]groupMask
	off := 0
	for n := 0; n < maxProcRecords && off+8 <= len(buf); n++ {
		rel := binary.LittleEndian.Uint32(buf[off:])
		size := int(binary.LittleEndian.Uint32(buf[off+4:]))
		if size < 8 || off+size > len(buf) {
			break
		}
		rec := buf[off : off+size]
		off += size
		if rel != relProcessorCore && rel != relProcessorPackage {
			continue
		}
		if len(rec) < 32 {
			continue
		}
		gc := int(binary.LittleEndian.Uint16(rec[30:]))
		if 32+16*gc > len(rec) {
			continue
		}
		masks := make([]groupMask, gc)
		for i := range masks {
			masks[i] = groupMask{
				mask:  binary.LittleEndian.Uint64(rec[32+16*i:]),
				group: binary.LittleEndian.Uint16(rec[32+16*i+8:]),
			}
		}
		if rel == relProcessorPackage {
			packages = append(packages, masks)
		} else {
			cores = append(cores, masks)
		}
	}
	if len(packages) == 0 {
		return nil, false
	}
	out := make([]socketCounts, len(packages))
	for _, c := range cores {
		if len(c) == 0 {
			continue
		}
		for i, p := range packages {
			if t := overlap(p, c); t > 0 {
				out[i].cores++
				out[i].threads += t
				break
			}
		}
	}
	return out, true
}

// overlap counts the bits of core that fall inside the package masks of the same group.
func overlap(pkg, core []groupMask) int {
	total := 0
	for _, c := range core {
		for _, p := range pkg {
			if p.group == c.group {
				total += bits.OnesCount64(p.mask & c.mask)
			}
		}
	}
	return total
}

type storageDesc struct {
	vendor, product, revision, serial string
	bus                               uint32
}

// parseStorageDescriptor reads a STORAGE_DEVICE_DESCRIPTOR; an offset outside the buffer gives an empty string.
func parseStorageDescriptor(b []byte) (storageDesc, bool) {
	if len(b) < 32 {
		return storageDesc{}, false
	}
	str := func(at int) string {
		off := binary.LittleEndian.Uint32(b[at:])
		if off == 0 || uint64(off) >= uint64(len(b)) {
			return ""
		}
		rest := b[off:]
		if i := indexNul(rest); i >= 0 {
			rest = rest[:i]
		}
		if len(rest) > maxStringLen {
			rest = rest[:maxStringLen]
		}
		return strings.TrimSpace(string(rest))
	}
	return storageDesc{
		vendor: str(12), product: str(16), revision: str(20), serial: str(24),
		bus: binary.LittleEndian.Uint32(b[28:]),
	}, true
}

func indexNul(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}
