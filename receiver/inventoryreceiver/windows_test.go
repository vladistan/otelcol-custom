// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWin struct {
	firmware  []byte
	firmErr   error
	strs      map[string]string
	nums      map[string]uint64
	subs      map[string][]string
	procInfo  []byte
	procErr   error
	drives    map[int]winDrive
	driveErrs map[int]error

	totalMem    uint64
	totalMemErr error
}

func (f *fakeWin) physicalMemory() (uint64, error) { return f.totalMem, f.totalMemErr }

func (f *fakeWin) firmwareTable() ([]byte, error) { return f.firmware, f.firmErr }
func (f *fakeWin) registryString(key, name string) (string, error) {
	if v, ok := f.strs[key+"|"+name]; ok {
		return v, nil
	}
	return "", errWinNotFound
}
func (f *fakeWin) registryNumber(key, name string) (uint64, error) {
	if v, ok := f.nums[key+"|"+name]; ok {
		return v, nil
	}
	return 0, errWinNotFound
}
func (f *fakeWin) registrySubKeys(key string) ([]string, error) {
	if v, ok := f.subs[key]; ok {
		return v, nil
	}
	return nil, errWinNotFound
}
func (f *fakeWin) processorInfo() ([]byte, error) { return f.procInfo, f.procErr }
func (f *fakeWin) drive(i int) (winDrive, error) {
	if err := f.driveErrs[i]; err != nil {
		return winDrive{}, err
	}
	if d, ok := f.drives[i]; ok {
		return d, nil
	}
	return winDrive{}, errWinNotFound
}

func procRecord(rel uint32, masks ...uint64) []byte {
	size := 32 + 16*len(masks)
	b := make([]byte, size)
	binary.LittleEndian.PutUint32(b[0:], rel)
	binary.LittleEndian.PutUint32(b[4:], uint32(size))
	binary.LittleEndian.PutUint16(b[30:], uint16(len(masks)))
	for i, m := range masks {
		binary.LittleEndian.PutUint64(b[32+16*i:], m)
	}
	return b
}

func storageDescriptor(bus uint32, vendor, product, rev, serial string) []byte {
	b := make([]byte, 40)
	put := func(off int, s string) {
		if s == "" {
			return
		}
		binary.LittleEndian.PutUint32(b[off:], uint32(len(b)))
		b = append(b, s...)
		b = append(b, 0)
	}
	put(12, vendor)
	put(16, product)
	put(20, rev)
	put(24, serial)
	binary.LittleEndian.PutUint32(b[28:], bus)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)))
	return b
}

func physicalWin() *fakeWin {
	table := joinTable(
		structure(0, []byte{1, 2, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Vendor Test", "F.20", "01/02/2023"),
		structure(1, []byte{1, 2, 3, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "HP", "Z4 G4", "v1", "WSER-1"),
		structure(2, []byte{1, 2, 3, 4, 5}, "HP", "81C5", "KBC", "BSER-2", "To Be Filled By O.E.M."),
		goodDimm(),
		dimmStructure(dimmSpec{size: 0, locator: 1}, "DIMM_A2"),
		endMarker(),
	)
	raw := append([]byte{0, 3, 3, 0, 0, 0, 0, 0}, table...)
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table)))
	return &fakeWin{
		firmware: raw,
		totalMem: 68719476736,
		strs: map[string]string{
			winCPUKey + `\0|ProcessorNameString`: "Intel(R) Xeon(R) W-2123 CPU @ 3.60GHz",
			winCPUKey + `\0|VendorIdentifier`:    "GenuineIntel",
			winGPUKey + `\0000|DriverDesc`:       "NVIDIA Quadro P2000",
			winGPUKey + `\0000|DriverVersion`:    "31.0.15.3623",
			winGPUKey + `\0000|MatchingDeviceId`: `pci\ven_10de&dev_1c30&subsys_11b310de`,
			winGPUKey + `\0001|DriverDesc`:       "Microsoft Basic Display Adapter",
		},
		nums: map[string]uint64{
			winCPUKey + `\0|~MHz`:                                3600,
			winGPUKey + `\0000|HardwareInformation.qwMemorySize`: 5368709120,
		},
		subs: map[string][]string{winCPUKey: {"0", "1"}, winGPUKey: {"0000", "0001", "Properties"}},
		procInfo: joinTable(
			procRecord(3, 0xFF),
			procRecord(0, 0x03), procRecord(0, 0x0C), procRecord(0, 0x30), procRecord(0, 0xC0),
		),
		drives: map[int]winDrive{
			0: {descriptor: storageDescriptor(17, "", "Samsung SSD 970 EVO 1TB", "2B2QEXE7", "0025_38B1_21E0_29F2."), size: 1000204886016, rotational: false, rotationalKnown: true},
			2: {descriptor: storageDescriptor(11, "ATA", "ST4000DM004", "0001", "ZFN0ABCD"), size: 4000787030016, rotational: true, rotationalKnown: true},
			3: {descriptor: storageDescriptor(14, "Msft", "Virtual Disk", "1.0", ""), size: 1 << 30},
		},
	}
}

func readWin(w *fakeWin) inventory {
	return (&windowsSource{api: w, maxDrives: 64}).read(context.Background())
}

func TestWindowsPhysicalHostGivesEveryFact(t *testing.T) {
	inv := readWin(physicalWin())

	assert.Equal(t, hostPhysical, inv.host)
	cpu := oneFact(t, inv, metricCPU)
	assert.Equal(t, "Intel(R) Xeon(R) W-2123 CPU @ 3.60GHz", cpu.attrs["hw.model"])
	assert.Equal(t, int64(1), cpu.attrs["cpu.sockets"])
	assert.Equal(t, int64(4), cpu.attrs["cpu.cores"])
	assert.Equal(t, int64(8), cpu.attrs["cpu.threads"])
	assert.Equal(t, int64(3_600_000_000), cpu.attrs["cpu.frequency.nominal"])
	assert.NotContains(t, cpu.attrs, "cpu.frequency.max", "the registry has no maximum")

	mem := factsOf(inv, metricMemory)
	require.Len(t, mem, 1)
	assert.Equal(t, "DIMM_A1", mem[0].attrs["hw.id"])

	assert.Equal(t, "81C5", oneFact(t, inv, metricBoard).attrs["hw.model"])
	assert.NotContains(t, oneFact(t, inv, metricBoard).attrs, "hw.asset_tag")
	assert.Equal(t, "F.20", oneFact(t, inv, metricBIOS).attrs["hw.firmware_version"])
	assert.Equal(t, "WSER-1", oneFact(t, inv, metricSystem).attrs["hw.serial_number"])

	drives := factsOf(inv, metricDrive)
	require.Len(t, drives, 2, "a virtual disk is skipped")
	assert.Equal(t, "Samsung SSD 970 EVO 1TB", drives[0].attrs["hw.model"])
	assert.Equal(t, "2B2QEXE7", drives[0].attrs["hw.firmware_version"])
	assert.Equal(t, "nvme", drives[0].attrs["drive.bus"])
	assert.Equal(t, "SSD", drives[0].attrs["drive.type"])
	assert.Equal(t, int64(1000204886016), drives[0].attrs["drive.size"])
	assert.Equal(t, "HDD", drives[1].attrs["drive.type"])
	assert.Equal(t, "sata", drives[1].attrs["drive.bus"])
	assert.Equal(t, "ZFN0ABCD", drives[1].attrs["hw.serial_number"])

	gpu := oneFact(t, inv, metricGPU)
	assert.Equal(t, "NVIDIA Quadro P2000", gpu.attrs["hw.model"])
	assert.Equal(t, "31.0.15.3623", gpu.attrs["hw.driver_version"])
	assert.Equal(t, "10de", gpu.attrs["gpu.vendor_id"])
	assert.Equal(t, "1c30", gpu.attrs["gpu.device_id"])
	assert.Equal(t, int64(5368709120), gpu.attrs["gpu.memory"])

	for _, src := range []string{"cpu", "memory", "board", "bios", "system", "drive", "gpu"} {
		assert.True(t, inv.status[src], src)
	}
}

func TestWindowsVirtualMachineGivesNothing(t *testing.T) {
	w := physicalWin()
	table := joinTable(structure(1, []byte{1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Microsoft Corporation", "Virtual Machine"), endMarker())
	raw := append([]byte{0, 3, 3, 0, 0, 0, 0, 0}, table...)
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table)))
	w.firmware = raw

	inv := readWin(w)

	assert.Equal(t, hostVirtual, inv.host)
	assert.Empty(t, inv.facts)
	assert.Empty(t, inv.status)
}

func TestWindowsUnreadableSourcesGiveStatusZeroAndNoPanic(t *testing.T) {
	w := &fakeWin{firmErr: errors.New("access denied"), procErr: errors.New("failed"), driveErrs: map[int]error{0: errors.New("denied")}}

	var inv inventory
	require.NotPanics(t, func() { inv = readWin(w) })

	assert.Equal(t, hostUndecided, inv.host)
	assert.Empty(t, inv.facts)
	for _, src := range []string{"cpu", "memory", "board", "bios", "system", "drive", "gpu"} {
		assert.False(t, inv.status[src], src)
	}
	assert.True(t, inv.dmiUnreadable)
}

func TestWindowsMissingRegistryValuesLeaveAttributesOut(t *testing.T) {
	w := physicalWin()
	delete(w.strs, winCPUKey+`\0|ProcessorNameString`)
	delete(w.nums, winCPUKey+`\0|~MHz`)
	delete(w.strs, winGPUKey+`\0000|DriverVersion`)

	inv := readWin(w)

	cpu := oneFact(t, inv, metricCPU)
	assert.NotContains(t, cpu.attrs, "hw.model")
	assert.NotContains(t, cpu.attrs, "cpu.frequency.nominal")
	assert.NotContains(t, oneFact(t, inv, metricGPU).attrs, "hw.driver_version")
}

func TestWindowsDriveCap(t *testing.T) {
	w := physicalWin()
	w.drives = map[int]winDrive{}
	for i := 0; i < 20; i++ {
		w.drives[i] = winDrive{descriptor: storageDescriptor(11, "", fmt.Sprintf("Disk%02d", i), "1", fmt.Sprintf("S%02d", i)), size: 1 << 30}
	}

	inv := (&windowsSource{api: w, maxDrives: 5}).read(context.Background())

	assert.Len(t, factsOf(inv, metricDrive), 5)
	assert.True(t, inv.drivesCapped)
}

// The Windows calls cannot run in a unit test; these pin the codes and the access they need.
func TestWindowsLengthQueryNeedsReadAccessAndTheDriveIsOpenedWithIt(t *testing.T) {
	assert.Equal(t, uint32(0x7405C), ctlCode(7, 0x17, methodBuffered, fileReadAccess), "IOCTL_DISK_GET_LENGTH_INFO")
	assert.Equal(t, uint32(ioctlDiskLengthInfo), ctlCode(7, 0x17, methodBuffered, fileReadAccess))
	assert.Equal(t, uint32(ioctlStorageQuery), ctlCode(0x2D, 0x500, methodBuffered, fileAnyAccess), "IOCTL_STORAGE_QUERY_PROPERTY")

	access := (uint32(ioctlDiskLengthInfo) >> 14) & 3
	require.Equal(t, uint32(fileReadAccess), access)
	assert.Equal(t, uint32(genericRead), uint32(driveOpenAccess), "a handle with no access cannot send a read-access control code")
}

func TestWindowsDriveWithNoSizeKeepsItsFactsAndLeavesOneNoteWithoutIdentifiers(t *testing.T) {
	w := physicalWin()
	d := w.drives[2]
	d.size = 0
	w.drives[2] = d
	e := w.drives[0]
	e.size = 0
	w.drives[0] = e

	inv := readWin(w)

	drives := factsOf(inv, metricDrive)
	require.Len(t, drives, 2)
	for _, f := range drives {
		assert.NotContains(t, f.attrs, "drive.size")
	}
	assert.Equal(t, []string{"drive size is not readable"}, inv.notes)
	assert.True(t, inv.status["drive"])
}

func TestWindowsBMCVideoIsNotAGPU(t *testing.T) {
	w := physicalWin()
	w.subs[winGPUKey] = []string{"0000", "0002"}
	w.strs[winGPUKey+`\0002|DriverDesc`] = "ASPEED Graphics Family(WDDM)"
	w.strs[winGPUKey+`\0002|MatchingDeviceId`] = `pci\ven_1a03&dev_2000`

	inv := readWin(w)

	assert.Equal(t, "NVIDIA Quadro P2000", oneFact(t, inv, metricGPU).attrs["hw.model"])
}

func TestParseProcessorInfoCountsCoresAndThreadsPerSocket(t *testing.T) {
	buf := joinTable(
		procRecord(3, 0x0F), procRecord(3, 0xF0),
		procRecord(0, 0x01), procRecord(0, 0x02), procRecord(0, 0x04), procRecord(0, 0x08),
		procRecord(0, 0x10), procRecord(0, 0x20),
	)

	got, ok := parseProcessorInfo(buf)

	require.True(t, ok)
	assert.Equal(t, []socketCounts{{cores: 4, threads: 4}, {cores: 2, threads: 2}}, got)
}

func TestParseProcessorInfoRejectsDamagedBuffers(t *testing.T) {
	good := joinTable(procRecord(3, 0xFF), procRecord(0, 0x03))
	tests := map[string][]byte{
		"empty":            nil,
		"short header":     good[:5],
		"size zero":        {3, 0, 0, 0, 0, 0, 0, 0},
		"size beyond":      {3, 0, 0, 0, 0xFF, 0xFF, 0, 0, 1, 2},
		"truncated record": good[:len(good)-3],
	}
	for name, buf := range tests {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() { parseProcessorInfo(buf) })
		})
	}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 1000; i++ {
		b := make([]byte, rng.Intn(200))
		rng.Read(b)
		assert.NotPanics(t, func() { parseProcessorInfo(b) })
	}
}

func TestParseStorageDescriptor(t *testing.T) {
	d, ok := parseStorageDescriptor(storageDescriptor(17, "V", "Prod", "R1", "SN9"))

	require.True(t, ok)
	assert.Equal(t, "V", d.vendor)
	assert.Equal(t, "Prod", d.product)
	assert.Equal(t, "R1", d.revision)
	assert.Equal(t, "SN9", d.serial)
	assert.Equal(t, uint32(17), d.bus)
}

func TestParseStorageDescriptorOffsetsBeyondTheBufferAreAbsent(t *testing.T) {
	b := storageDescriptor(17, "V", "Prod", "R1", "SN9")
	binary.LittleEndian.PutUint32(b[24:], 9999)
	binary.LittleEndian.PutUint32(b[16:], 0xFFFFFFFF)

	d, ok := parseStorageDescriptor(b)

	require.True(t, ok)
	assert.Empty(t, d.serial)
	assert.Empty(t, d.product)
	assert.Equal(t, "V", d.vendor)

	_, ok = parseStorageDescriptor(b[:10])
	assert.False(t, ok)
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 1000; i++ {
		r := make([]byte, rng.Intn(120))
		rng.Read(r)
		assert.NotPanics(t, func() { parseStorageDescriptor(r) })
	}
}
