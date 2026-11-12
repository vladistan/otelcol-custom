//go:build windows

package nvmladapter

import (
	"errors"

	"golang.org/x/sys/windows"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

const libraryName = "nvml.dll"

// NewPort returns the uninitialised adapter. nvml.dll loads at the first Init.
// An empty libraryPath loads nvml.dll from System32 only; otherwise the path
// must be absolute, so the DLL search order never reaches the working
// directory or PATH.
func NewPort(libraryPath string) (port.Port, error) {
	var dll *windows.LazyDLL
	switch {
	case libraryPath == "":
		dll = windows.NewLazySystemDLL(libraryName)
	case IsWindowsAbsPath(libraryPath):
		dll = windows.NewLazyDLL(libraryPath)
	default:
		return nil, errors.New("gpu receiver: library_path must be an absolute path")
	}
	return &adapter{dll: dll, procs: newProcs(dll)}, nil
}
