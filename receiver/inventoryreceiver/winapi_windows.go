// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package inventoryreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// systemAPI calls the Windows interfaces the source needs. It is not covered by
// unit tests; the parsers it feeds are.
type systemAPI struct{}

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemFirmwareTable = kernel32.NewProc("GetSystemFirmwareTable")
	procGetLogicalProcessorEx  = kernel32.NewProc("GetLogicalProcessorInformationEx")
	procGlobalMemoryStatusEx   = kernel32.NewProc("GlobalMemoryStatusEx")
)

const (
	firmwareProviderRSMB = 0x52534D42 // 'RSMB'
	relationAll          = 0xFFFF
	propDevice           = 0
	propSeekPenalty      = 7
)

func (systemAPI) firmwareTable() ([]byte, error) {
	if err := procGetSystemFirmwareTable.Find(); err != nil {
		return nil, err
	}
	n, _, _ := procGetSystemFirmwareTable.Call(firmwareProviderRSMB, 0, 0, 0)
	if n == 0 {
		return nil, errors.New("no SMBIOS firmware table")
	}
	if n > maxTableBytes+8 {
		return nil, errors.New("SMBIOS firmware table is too large")
	}
	buf := make([]byte, n)
	got, _, _ := procGetSystemFirmwareTable.Call(firmwareProviderRSMB, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(n))
	if got == 0 || got > uintptr(n) {
		return nil, errors.New("cannot read the SMBIOS firmware table")
	}
	return buf[:got], nil
}

func (systemAPI) processorInfo() ([]byte, error) {
	if err := procGetLogicalProcessorEx.Find(); err != nil {
		return nil, err
	}
	var n uint32
	r, _, e := procGetLogicalProcessorEx.Call(relationAll, 0, uintptr(unsafe.Pointer(&n)))
	if r != 0 || e != windows.ERROR_INSUFFICIENT_BUFFER || n == 0 || n > 1<<20 {
		return nil, errors.New("cannot size the processor information")
	}
	buf := make([]byte, n)
	r, _, e = procGetLogicalProcessorEx.Call(relationAll, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return nil, e
	}
	return buf[:n], nil
}

// memoryStatusEx is MEMORYSTATUSEX of sysinfoapi.h (64 bytes).
type memoryStatusEx struct {
	length, memoryLoad                                 uint32
	totalPhys, availPhys, totalPageFile, availPageFile uint64
	totalVirtual, availVirtual, availExtendedVirtual   uint64
}

// physicalMemory returns ullTotalPhys of GlobalMemoryStatusEx.
func (systemAPI) physicalMemory() (uint64, error) {
	if err := procGlobalMemoryStatusEx.Find(); err != nil {
		return 0, err
	}
	st := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0, e
	}
	return st.totalPhys, nil
}

func openKey(key string) (registry.Key, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, errWinNotFound
	}
	return k, err
}

func (systemAPI) registryString(key, name string) (string, error) {
	k, err := openKey(key)
	if err != nil {
		return "", err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", errWinNotFound
	}
	return v, err
}

func (systemAPI) registryNumber(key, name string) (uint64, error) {
	k, err := openKey(key)
	if err != nil {
		return 0, err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetIntegerValue(name)
	if err == nil {
		return v, nil
	}
	if errors.Is(err, registry.ErrNotExist) {
		return 0, errWinNotFound
	}
	if raw, _, binErr := k.GetBinaryValue(name); binErr == nil && len(raw) == 8 {
		return binary.LittleEndian.Uint64(raw), nil
	}
	return 0, err
}

func (systemAPI) registrySubKeys(key string) ([]string, error) {
	k, err := openKey(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = k.Close() }()
	return k.ReadSubKeyNames(4096)
}

// openDrive opens a physical drive for the length query (read access). When that is refused it opens
// with no access rights, which is enough for the property queries; the size is then left out.
func openDrive(name *uint16) (windows.Handle, error) {
	const share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
	h, err := windows.CreateFile(name, driveOpenAccess, share, nil, windows.OPEN_EXISTING, 0, 0)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) && driveOpenAccess != 0 {
		return windows.CreateFile(name, 0, share, nil, windows.OPEN_EXISTING, 0, 0)
	}
	return h, err
}

// drive asks one physical drive for its descriptor, size and seek penalty.
func (systemAPI) drive(index int) (winDrive, error) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, index))
	if err != nil {
		return winDrive{}, err
	}
	h, err := openDrive(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return winDrive{}, errWinNotFound
		}
		return winDrive{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var d winDrive
	d.descriptor, err = storageQuery(h, propDevice, 2048)
	if err != nil {
		return winDrive{}, err
	}
	if b, err := storageQuery(h, propSeekPenalty, 64); err == nil && len(b) >= 9 {
		d.rotational, d.rotationalKnown = b[8] != 0, true
	}
	var length [8]byte
	var got uint32
	if err := windows.DeviceIoControl(h, ioctlDiskLengthInfo, nil, 0, &length[0], uint32(len(length)), &got, nil); err == nil && got == 8 {
		d.size = binary.LittleEndian.Uint64(length[:])
	}
	return d, nil
}

func storageQuery(h windows.Handle, property uint32, size int) ([]byte, error) {
	var in [12]byte
	binary.LittleEndian.PutUint32(in[0:], property)
	out := make([]byte, size)
	var got uint32
	if err := windows.DeviceIoControl(h, ioctlStorageQuery, &in[0], uint32(len(in)), &out[0], uint32(len(out)), &got, nil); err != nil {
		return nil, err
	}
	return out[:got], nil
}
