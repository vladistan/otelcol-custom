package nvmladapter

import (
	"bytes"
	"strconv"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

// NVML return codes (nvmlReturn_t in nvml.h) that the adapters map to a port.Kind.
const (
	retSuccess                 = 0
	retNotSupported            = 3
	retNoPermission            = 4
	retDriverNotLoaded         = 9
	retLibraryNotFound         = 12
	retGPUIsLost               = 15
	retLibRMVersionMismatch    = 18
	retFreqNotSupported        = 24
	retArgumentVersionMismatch = 25
)

// nvmlReturn is a raw NVML return code used as the cause of a port.Error.
type nvmlReturn uint32

func (r nvmlReturn) Error() string { return "nvml return code " + strconv.FormatUint(uint64(r), 10) }

// kindOfCode folds frequency and argument-version codes into NotSupported and
// VersionMismatch; every code the table omits is Other.
func kindOfCode(code uint32) port.Kind {
	switch code {
	case retNotSupported, retFreqNotSupported:
		return port.NotSupported
	case retGPUIsLost:
		return port.GPULost
	case retLibraryNotFound:
		return port.LibraryNotFound
	case retDriverNotLoaded:
		return port.DriverNotLoaded
	case retNoPermission:
		return port.NoPermission
	case retLibRMVersionMismatch, retArgumentVersionMismatch:
		return port.VersionMismatch
	default:
		return port.Other
	}
}

// checkCode returns nil for NVML_SUCCESS and a port.Error otherwise.
func checkCode(code uint32) error {
	if code == retSuccess {
		return nil
	}
	return port.NewError(kindOfCode(code), nvmlReturn(code))
}

// pcieCounterOf returns the nvmlPcieUtilCounter_t value for a direction.
func pcieCounterOf(dir port.Direction) (uint32, bool) {
	switch dir {
	case port.Transmit:
		return 0, true // NVML_PCIE_UTIL_TX_BYTES
	case port.Receive:
		return 1, true // NVML_PCIE_UTIL_RX_BYTES
	default:
		return 0, false
	}
}

// eccErrorTypeOf returns the nvmlMemoryErrorType_t value for an ECC kind.
func eccErrorTypeOf(kind port.ECCKind) (uint32, bool) {
	switch kind {
	case port.Corrected:
		return 0, true // NVML_MEMORY_ERROR_TYPE_CORRECTED
	case port.Uncorrected:
		return 1, true // NVML_MEMORY_ERROR_TYPE_UNCORRECTED
	default:
		return 0, false
	}
}

// clockTypeOf returns the nvmlClockType_t value for a clock domain.
func clockTypeOf(domain port.ClockDomain) (uint32, bool) {
	switch domain {
	case port.ClockGraphics:
		return 0, true // NVML_CLOCK_GRAPHICS
	case port.ClockSM:
		return 1, true // NVML_CLOCK_SM
	case port.ClockMemory:
		return 2, true // NVML_CLOCK_MEM
	case port.ClockVideo:
		return 3, true // NVML_CLOCK_VIDEO
	default:
		return 0, false
	}
}

// utilization is nvmlUtilization_t: percent of time busy over the sample period.
type utilization struct {
	gpu    uint32
	memory uint32
}

// memoryInfo is nvmlMemory_t, in bytes. The field order is the header's.
type memoryInfo struct {
	total uint64
	free  uint64
	used  uint64
}

// cString returns the text of a C char buffer up to the first NUL.
func cString(buf []byte) string {
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}

// IsWindowsAbsPath reports whether path is a drive-letter or UNC absolute
// Windows path. It does not use filepath so it gives the same answer on every OS.
func IsWindowsAbsPath(path string) bool {
	if len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && isSlash(path[2]) {
		return true
	}
	return len(path) >= 2 && path[0] == '\\' && path[1] == '\\'
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isSlash(c byte) bool { return c == '\\' || c == '/' }
