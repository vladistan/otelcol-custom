// Package nvmladapter wraps NVML behind the port contract: go-nvml on linux
// with cgo, nvml.dll loaded without cgo on windows. Every other build gets a
// constructor that reports the platform as unsupported.
package nvmladapter

import "errors"

// ErrPlatformUnsupported is returned by NewPort on builds that are neither
// linux with cgo nor windows.
var ErrPlatformUnsupported = errors.New("gpu receiver: platform unsupported, NVML needs linux with cgo or windows")
